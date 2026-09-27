package pxe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mikael/kubit/internal/fsx"
	"github.com/mikael/kubit/internal/httpx"
)

var ipxeURLs = map[string]string{
	FileBIOS:  "https://boot.ipxe.org/undionly.kpxe",
	FileX64:   "https://boot.ipxe.org/x86_64-efi/ipxe.efi",
	FileARM64: "https://boot.ipxe.org/arm64-efi/ipxe.efi",
}

const (
	currentTTL        = 24 * time.Hour
	downloadBudget    = 10 * time.Minute
	staleWait         = 30 * time.Second
	revalidateBackoff = 10 * time.Minute
)

type Cache struct {
	Dir       string
	staleWait time.Duration
	backoff   time.Duration
	mu        sync.Mutex
	inflight  map[string]*download
	retry     map[string]time.Time
}

type download struct {
	done chan struct{}
	err  error
}

func (d *download) wait(ctx context.Context) error {
	select {
	case <-d.done:
		return d.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func NewCache(dir string) *Cache {
	return &Cache{Dir: dir, staleWait: staleWait, backoff: revalidateBackoff, inflight: map[string]*download{}, retry: map[string]time.Time{}}
}

func cacheName(url string) string {
	return strings.NewReplacer("https://", "", "http://", "", "/", "_").Replace(url)
}

func (c *Cache) local(url string) string { return filepath.Join(c.Dir, cacheName(url)) }

func (c *Cache) Path(ctx context.Context, url string) (string, error) {
	path := c.local(url)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	d := c.start(ctx, path, func(ctx context.Context) error {
		if err := os.MkdirAll(c.Dir, 0o700); err != nil {
			return err
		}
		return httpx.FetchFile(ctx, httpx.Download, url, path, 0o644)
	})
	if err := d.wait(ctx); err != nil {
		return "", err
	}
	return path, nil
}

func (c *Cache) Current(ctx context.Context, urls []string, revalidate bool) ([]string, error) {
	paths := make([]string, len(urls))
	for i, u := range urls {
		paths[i] = c.local(u)
	}
	key := strings.Join(paths, "\x00")
	metas, cached := readMetas(paths)
	now := time.Now()
	if cached && (!revalidate || fresh(metas, now) || c.backingOff(key, now)) {
		return paths, nil
	}
	d := c.start(ctx, key, func(ctx context.Context) error { return c.refresh(ctx, urls, paths, metas, cached) })
	if !cached {
		if err := d.wait(ctx); err != nil {
			return nil, err
		}
		return paths, nil
	}
	timer := time.NewTimer(c.staleWait)
	defer timer.Stop()
	select {
	case <-d.done:
		if d.err == nil {
			return paths, nil
		}
	case <-timer.C:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	c.mu.Lock()
	c.retry[key] = time.Now().Add(c.backoff)
	c.mu.Unlock()
	return paths, nil
}

func (c *Cache) backingOff(key string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return now.Before(c.retry[key])
}

func (c *Cache) start(ctx context.Context, key string, fetch func(context.Context) error) *download {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d, busy := c.inflight[key]; busy {
		return d
	}
	d := &download{done: make(chan struct{})}
	c.inflight[key] = d
	go func() {
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), downloadBudget)
		defer cancel()
		d.err = fetch(dctx)
		c.mu.Lock()
		delete(c.inflight, key)
		c.mu.Unlock()
		close(d.done)
	}()
	return d
}

type meta struct {
	httpx.Validators
	Checked time.Time `json:"checked"`
}

func metaPath(path string) string { return path + ".meta" }

func stagePath(path string) string {
	return filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".next.part")
}

func readMetas(paths []string) ([]meta, bool) {
	metas := make([]meta, len(paths))
	cached := true
	for i, p := range paths {
		if _, err := os.Stat(p); err != nil {
			cached = false
			continue
		}
		if b, err := os.ReadFile(metaPath(p)); err == nil {
			_ = json.Unmarshal(b, &metas[i])
		}
	}
	return metas, cached
}

func fresh(metas []meta, now time.Time) bool {
	for _, m := range metas {
		if now.Sub(m.Checked) >= currentTTL || m.Checked.After(now) {
			return false
		}
	}
	return true
}

func (c *Cache) refresh(ctx context.Context, urls, paths []string, metas []meta, cached bool) error {
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return err
	}
	client := httpx.Download
	if cached {
		client = httpx.Revalidate
	}
	staged := make([]string, len(urls))
	defer func() {
		for _, s := range staged {
			if s != "" {
				os.Remove(s)
			}
		}
	}()
	next := slices.Clone(metas)
	for i, url := range urls {
		var got httpx.Validators
		err := fsx.WriteStream(stagePath(paths[i]), 0o644, func(w io.Writer) error {
			var err error
			got, err = httpx.FetchIfChanged(ctx, client, url, metas[i].Validators, w)
			return err
		})
		switch {
		case errors.Is(err, httpx.ErrNotModified):
		case err != nil:
			return err
		default:
			staged[i], next[i].Validators = stagePath(paths[i]), got
		}
	}
	for i, s := range staged {
		if s == "" {
			continue
		}
		if err := os.Rename(s, paths[i]); err != nil {
			return err
		}
		staged[i] = ""
	}
	now := time.Now()
	for i, p := range paths {
		next[i].Checked = now
		b, err := json.Marshal(next[i])
		if err != nil {
			return err
		}
		if err := fsx.WriteFile(metaPath(p), b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (c *Cache) IPXEBinary(ctx context.Context, name string) (string, error) {
	url, ok := ipxeURLs[name]
	if !ok {
		return "", fmt.Errorf("%s: not a boot file", name)
	}
	return c.Path(ctx, url)
}

var factoryAsset = regexp.MustCompile(`^(.+_image_[0-9a-f]+)_(v[^_]+)_((?:kernel|initramfs)-[^_]+)$`)

func (c *Cache) Tidy(keep int, pinned string, partAge time.Duration) error {
	if err := fsx.SweepParts(c.Dir, partAge); err != nil {
		return err
	}
	entries, err := os.ReadDir(c.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	versions := map[string][]string{}
	files := map[string][]string{}
	for _, e := range entries {
		m := factoryAsset.FindStringSubmatch(e.Name())
		if e.IsDir() || m == nil {
			continue
		}
		key := m[1] + "\x00" + m[2]
		if len(files[key]) == 0 {
			versions[m[1]] = append(versions[m[1]], m[2])
		}
		files[key] = append(files[key], e.Name())
	}
	var errs []error
	for schematic, vs := range versions {
		slices.SortFunc(vs, func(a, b string) int { return compareVersions(b, a) })
		for _, v := range vs[min(keep, len(vs)):] {
			if v == pinned {
				continue
			}
			for _, name := range files[schematic+"\x00"+v] {
				if err := os.Remove(filepath.Join(c.Dir, name)); err != nil && !os.IsNotExist(err) {
					errs = append(errs, err)
				}
			}
		}
	}
	return errors.Join(errs...)
}

func compareVersions(a, b string) int {
	pa, pb := versionParts(a), versionParts(b)
	for i := range 3 {
		if pa.n[i] != pb.n[i] {
			return pa.n[i] - pb.n[i]
		}
	}
	switch {
	case pa.pre == pb.pre:
		return 0
	case pa.pre == "":
		return 1
	case pb.pre == "":
		return -1
	}
	return strings.Compare(pa.pre, pb.pre)
}

type version struct {
	n   [3]int
	pre string
}

func versionParts(s string) version {
	var v version
	core, pre, _ := strings.Cut(strings.TrimPrefix(s, "v"), "-")
	v.pre = pre
	for i, p := range strings.SplitN(core, ".", 3) {
		v.n[i], _ = strconv.Atoi(p)
	}
	return v
}
