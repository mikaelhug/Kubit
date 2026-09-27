package pxe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type mirror struct {
	mu       sync.Mutex
	files    map[string]string
	modified time.Time
	gets     int
	matched  int
	hold     chan struct{}
	down     bool
}

func (m *mirror) set(files map[string]string, modified time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files, m.modified = files, modified
}

func (m *mirror) counts() (int, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.gets, m.matched
}

func (m *mirror) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	m.gets++
	hold, down := m.hold, m.down
	body, modified := m.files[path.Base(r.URL.Path)], m.modified
	etag := fmt.Sprintf("%q", body)
	if r.Header.Get("If-None-Match") == etag {
		m.matched++
	}
	m.mu.Unlock()
	if hold != nil {
		<-hold
	}
	if down {
		http.Error(w, "down", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("ETag", etag)
	http.ServeContent(w, r, "", modified, strings.NewReader(body))
}

func readAll(t *testing.T, paths []string) string {
	t.Helper()
	var out []string
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, string(b))
	}
	return strings.Join(out, "+")
}

func age(t *testing.T, paths []string, by time.Duration) {
	t.Helper()
	for _, p := range paths {
		var m meta
		b, err := os.ReadFile(metaPath(p))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		m.Checked = m.Checked.Add(-by)
		b, _ = json.Marshal(m)
		if err := os.WriteFile(metaPath(p), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDebianPairIsRevalidatedFromStoredValidators(t *testing.T) {
	m := &mirror{}
	m.set(map[string]string{"linux": "k1", "initrd.gz": "i1"}, time.Now().Add(-48*time.Hour).UTC().Truncate(time.Second))
	srv := httptest.NewServer(m)
	defer srv.Close()
	c := NewCache(t.TempDir())
	ctx := context.Background()
	pair := []string{srv.URL + "/debian/current/linux", srv.URL + "/debian/current/initrd.gz"}

	p, err := c.Current(ctx, pair, true)
	if err != nil || readAll(t, p) != "k1+i1" {
		t.Fatalf("first fetch: %v", err)
	}
	if _, err := c.Current(ctx, pair, true); err != nil {
		t.Fatal(err)
	}
	if gets, _ := m.counts(); gets != 2 {
		t.Fatalf("a fresh pair is served from the cache (%d requests)", gets)
	}
	for _, f := range p {
		old := time.Now().Add(-72 * time.Hour)
		os.Chtimes(f, old, old)
	}
	if _, err := c.Current(ctx, pair, true); err != nil {
		t.Fatal(err)
	}
	if gets, _ := m.counts(); gets != 2 {
		t.Errorf("freshness comes from the sidecar, not the file mtime (%d requests)", gets)
	}

	age(t, p, currentTTL+time.Minute)
	if _, err := c.Current(ctx, pair, false); err != nil {
		t.Fatal(err)
	}
	if gets, _ := m.counts(); gets != 2 {
		t.Errorf("the initrd request never revalidates the pair (%d requests)", gets)
	}
	if _, err := c.Current(ctx, pair, true); err != nil || readAll(t, p) != "k1+i1" {
		t.Fatalf("revalidate: %v", err)
	}
	if gets, matched := m.counts(); gets != 4 || matched != 2 {
		t.Fatalf("a stale pair is revalidated with the stored ETags: %d requests, %d matched", gets, matched)
	}

	age(t, p, currentTTL+time.Minute)
	m.set(map[string]string{"linux": "k2", "initrd.gz": "i2"}, time.Now().Add(-30*24*time.Hour).UTC().Truncate(time.Second))
	if _, err := c.Current(ctx, pair, true); err != nil || readAll(t, p) != "k2+i2" {
		t.Fatalf("a point release with an older Last-Modified replaces the pair: %v %q", err, readAll(t, p))
	}
	if left, _ := filepath.Glob(filepath.Join(c.Dir, ".*")); len(left) != 0 {
		t.Errorf("staged files left behind: %v", left)
	}

	var factoryGets atomic.Int32
	factory := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		factoryGets.Add(1)
		fmt.Fprint(w, "kernel")
	}))
	defer factory.Close()
	asset := factory.URL + "/image/abc/v1.14.0/kernel-amd64"
	fp, err := c.Path(ctx, asset)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-365 * 24 * time.Hour)
	os.Chtimes(fp, old, old)
	if _, err := c.Path(ctx, asset); err != nil || factoryGets.Load() != 1 {
		t.Errorf("content-addressed assets are cached forever (%d requests)", factoryGets.Load())
	}
}

func TestDebianPairSwapsOnlyWhenBothArrive(t *testing.T) {
	m := &mirror{}
	m.set(map[string]string{"linux": "k1", "initrd.gz": "i1"}, time.Now().UTC().Truncate(time.Second))
	srv := httptest.NewServer(m)
	defer srv.Close()
	c := NewCache(t.TempDir())
	pair := []string{srv.URL + "/linux", srv.URL + "/initrd.gz"}
	p, err := c.Current(t.Context(), pair, true)
	if err != nil {
		t.Fatal(err)
	}
	age(t, p, currentTTL+time.Minute)
	m.set(map[string]string{"linux": "k2"}, time.Now().UTC().Truncate(time.Second))
	if _, err := c.Current(t.Context(), pair, true); err != nil || readAll(t, p) != "k1+i1" {
		t.Errorf("a half-updated mirror keeps the old pair: %v %q", err, readAll(t, p))
	}
}

func TestStaleCopyIsServedWhenTheMirrorIsSlowOrDown(t *testing.T) {
	m := &mirror{}
	m.set(map[string]string{"linux": "k1", "initrd.gz": "i1"}, time.Now().UTC().Truncate(time.Second))
	srv := httptest.NewServer(m)
	defer srv.Close()
	c := NewCache(t.TempDir())
	c.staleWait = 200 * time.Millisecond
	pair := []string{srv.URL + "/linux", srv.URL + "/initrd.gz"}
	p, err := c.Current(t.Context(), pair, true)
	if err != nil {
		t.Fatal(err)
	}

	age(t, p, currentTTL+time.Minute)
	hold := make(chan struct{})
	m.mu.Lock()
	m.hold = hold
	m.mu.Unlock()
	start := time.Now()
	if got, err := c.Current(t.Context(), pair, true); err != nil || readAll(t, got) != "k1+i1" {
		t.Fatalf("a slow mirror serves the stale pair: %v", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("waited %s for a slow mirror", took)
	}
	before, _ := m.counts()
	start = time.Now()
	if _, err := c.Current(t.Context(), pair, true); err != nil || time.Since(start) > 100*time.Millisecond {
		t.Errorf("a retry inside the backoff does not wait again: %v %s", err, time.Since(start))
	}
	if after, _ := m.counts(); after != before {
		t.Errorf("no request inside the backoff (%d, then %d)", before, after)
	}
	m.mu.Lock()
	m.hold = nil
	m.mu.Unlock()
	close(hold)
	waitIdle(t, c)
	before, _ = m.counts()
	if _, err := c.Current(t.Context(), pair, true); err != nil {
		t.Fatal(err)
	}
	if after, _ := m.counts(); after != before {
		t.Errorf("the background revalidation finished and marked the pair fresh (%d, then %d)", before, after)
	}

	age(t, p, currentTTL+time.Minute)
	c.retry = map[string]time.Time{}
	m.mu.Lock()
	m.down = true
	m.mu.Unlock()
	if got, err := c.Current(t.Context(), pair, true); err != nil || readAll(t, got) != "k1+i1" {
		t.Errorf("a failing mirror serves the stale pair: %v", err)
	}
	before, _ = m.counts()
	if _, err := c.Current(t.Context(), pair, true); err != nil {
		t.Fatal(err)
	}
	if after, _ := m.counts(); after != before {
		t.Errorf("a failure backs off (%d, then %d)", before, after)
	}
	srv.Close()
	c.retry = map[string]time.Time{}
	if got, err := c.Current(t.Context(), pair, true); err != nil || readAll(t, got) != "k1+i1" {
		t.Errorf("an unreachable mirror serves the stale pair: %v", err)
	}
	if _, err := NewCache(t.TempDir()).Current(t.Context(), pair, true); err == nil {
		t.Error("without a cached copy an unreachable mirror is an error")
	}
}

func waitIdle(t *testing.T, c *Cache) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		n := len(c.inflight)
		c.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("download still in flight")
}

func TestTidyKeepsTheNewestVersionsAndSweepsParts(t *testing.T) {
	dir := t.TempDir()
	c := NewCache(dir)
	schematic := "376567988ad370138ad8b2698212367b8edcb69b5fd68c80be1f2ec7d603b4ba"
	var names []string
	for _, v := range []string{"v1.9.5", "v1.10.0", "v1.10.0-beta.1", "v1.14.0", "v1.13.2"} {
		for _, f := range []string{"kernel-amd64", "initramfs-amd64.xz"} {
			names = append(names, "factory.talos.dev_image_"+schematic+"_"+v+"_"+f)
		}
	}
	names = append(names, "factory.talos.dev_image_abc_v1.1.0_kernel-arm64", "boot.ipxe.org_undionly.kpxe", "deb.debian.org_debian_dists_trixie_main_installer-amd64_current_images_netboot_debian-installer_amd64_linux")
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * time.Hour)
	part := filepath.Join(dir, ".boot.ipxe.org_ipxe.efi.123.part")
	os.WriteFile(part, []byte("x"), 0o644)
	os.Chtimes(part, old, old)
	if err := c.Tidy(3, "v1.9.5", time.Hour); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	var left []string
	for _, e := range entries {
		left = append(left, e.Name())
	}
	sort.Strings(left)
	want := []string{
		"boot.ipxe.org_undionly.kpxe",
		"deb.debian.org_debian_dists_trixie_main_installer-amd64_current_images_netboot_debian-installer_amd64_linux",
		"factory.talos.dev_image_" + schematic + "_v1.10.0_initramfs-amd64.xz",
		"factory.talos.dev_image_" + schematic + "_v1.10.0_kernel-amd64",
		"factory.talos.dev_image_" + schematic + "_v1.13.2_initramfs-amd64.xz",
		"factory.talos.dev_image_" + schematic + "_v1.13.2_kernel-amd64",
		"factory.talos.dev_image_" + schematic + "_v1.14.0_initramfs-amd64.xz",
		"factory.talos.dev_image_" + schematic + "_v1.14.0_kernel-amd64",
		"factory.talos.dev_image_" + schematic + "_v1.9.5_initramfs-amd64.xz",
		"factory.talos.dev_image_" + schematic + "_v1.9.5_kernel-amd64",
		"factory.talos.dev_image_abc_v1.1.0_kernel-arm64",
	}
	if fmt.Sprint(left) != fmt.Sprint(want) {
		t.Errorf("left after tidy:\n%s\nwant:\n%s", left, want)
	}
	if err := NewCache(filepath.Join(dir, "missing")).Tidy(3, "", time.Hour); err != nil {
		t.Errorf("a missing cache is not an error: %v", err)
	}
}

func TestCompareVersions(t *testing.T) {
	for _, c := range [][2]string{{"v1.10.0", "v1.9.9"}, {"v1.14.0", "v1.14.0-beta.1"}, {"v2.0.0", "v1.99.99"}, {"v1.14.1", "v1.14.0"}} {
		if compareVersions(c[0], c[1]) <= 0 || compareVersions(c[1], c[0]) >= 0 {
			t.Errorf("%s must sort after %s", c[0], c[1])
		}
	}
	if compareVersions("v1.14.0", "v1.14.0") != 0 {
		t.Error("equal versions")
	}
}
