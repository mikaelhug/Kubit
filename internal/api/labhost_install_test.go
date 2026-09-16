package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/oob/ider"
	"github.com/mikael/kubit/internal/store"
)

func newTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	c, _ := store.NewCrypto(bytes.Repeat([]byte{8}, 32))
	dir := t.TempDir()
	st, err := store.Open(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return New("test", cluster.NewManager(st, dir), "", c), st
}

// The boot phase turns IDE-R events into the operator's picture: no first read
// within the budget names the boot override, a closed session names the session.
func TestMediaBootPhases(t *testing.T) {
	labBootWait, labPollEvery = 200*time.Millisecond, 20*time.Millisecond
	var logs []string
	sink := func(e clusterEvent) {
		if e.Kind == "log" {
			logs = append(logs, e.Message)
		}
	}
	b := &mediaBoot{events: make(chan ider.Event, 16), done: make(chan error, 1), cancel: func() {}, sink: sink, mac: "aa"}
	b.events <- ider.Event{Kind: ider.Authenticated}
	b.events <- ider.Event{Kind: ider.Opened, Buffer: 4096}
	err := phase(context.Background(), sink, b, "aa", "boot", labBootWait, func() (bool, string) {
		if b.firstRead {
			return true, "booted"
		}
		return false, "the machine never read the virtual CD"
	})
	if err == nil || !strings.Contains(err.Error(), "never read the virtual CD") {
		t.Fatalf("boot without reads: %v", err)
	}
	if len(logs) < 2 || !strings.Contains(logs[1], "virtual CD attached (4096-byte reads)") {
		t.Errorf("session events must reach the log: %v", logs)
	}
	b.events <- ider.Event{Kind: ider.FirstRead}
	b.events <- ider.Event{Kind: ider.Progress, Bytes: 3_000_000}
	if err := phase(context.Background(), sink, b, "aa", "boot", labBootWait, func() (bool, string) {
		if b.firstRead {
			return true, "booted"
		}
		return false, "x"
	}); err != nil || b.bytes != 3_000_000 {
		t.Fatalf("boot with a read: %v (%d bytes)", err, b.bytes)
	}
	b.events <- ider.Event{Kind: ider.Closed, Reason: "closed by AMT"}
	err = phase(context.Background(), sink, b, "aa", "load", time.Second, func() (bool, string) { return false, "waiting" })
	if err == nil || !strings.Contains(err.Error(), "redirection session ended (closed by AMT)") {
		t.Fatalf("closed session: %v", err)
	}
}

func TestInstallerFeed(t *testing.T) {
	s, st := newTestServer(t)
	ctx := context.Background()
	mac := "52:54:00:4c:41:01"
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/machines", strings.NewReader(`{"mac":"52:54:00:4C:41:01","ip":"192.168.105.20","arch":"arm64"}`)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("add machine: %d %s", rec.Code, rec.Body.String())
	}
	m, err := st.GetMachine(ctx, mac)
	if err != nil || m.Source != "manual" || m.Arch != "arm64" {
		t.Fatalf("manual row: %+v %v", m, err)
	}
	feed := s.feed.Handler()
	get := func(path, remote string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = "192.168.105.1:8069"
		req.RemoteAddr = remote
		feed.ServeHTTP(rec, req)
		return rec
	}
	if rec := get("/labhost/"+mac+"/preseed", "192.168.105.20:1"); rec.Code != http.StatusNotFound {
		t.Errorf("preseed for a machine that is not installing: %d", rec.Code)
	}
	_ = st.SetLabHost(ctx, mac, &store.LabHost{State: "installing"})
	rec = get("/labhost/"+mac+"/preseed?arch=arm64", "192.168.105.20:1")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "qemu-system-arm") || !strings.Contains(rec.Body.String(), "http://192.168.105.1:8069/labhost/"+mac+"/progress?stage=installer") {
		t.Errorf("preseed: %d %s", rec.Code, rec.Body.String()[:200])
	}
	if rec := get("/labhost/"+mac+"/progress?stage=packages", "192.168.105.21:1"); rec.Code != http.StatusNoContent {
		t.Fatalf("progress: %d %s", rec.Code, rec.Body.String())
	}
	m, _ = st.GetMachine(ctx, mac)
	if m.LabHost == nil || m.LabHost.Install == nil || m.LabHost.Install.Stage != "packages" || m.IP != "192.168.105.21" {
		t.Errorf("stage or address not recorded: %+v ip=%s", m.LabHost, m.IP)
	}
	if rec := get("/labhost/"+mac+"/postinstall", "192.168.105.21:1"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "/labhost/"+mac+"/progress?stage=booted") {
		t.Errorf("postinstall: %d", rec.Code)
	}
	_ = st.SetLabHost(ctx, mac, &store.LabHost{State: "ready"})
	if rec := get("/labhost/"+mac+"/progress?stage=booted", "192.168.105.21:1"); rec.Code != http.StatusNotFound {
		t.Errorf("a ready host must not accept progress: %d", rec.Code)
	}
}

func TestAddVMsControlPlaneSizing(t *testing.T) {
	r := addVMsRequest{Count: 4, CPUs: 2, MemMiB: 1536, DiskGiB: 20, ControlPlanes: 1, ControlPlaneMemMiB: 2048}
	sz := r.sizes()
	if sz[0].MemMiB != 2048 || sz[0].Role != "controlplane" || sz[1].MemMiB != 1536 || r.totalMem() != 2048+3*1536 || r.controlPlanes() != 1 {
		t.Errorf("uniform sizing: %+v", sz)
	}
	big := addVMsRequest{Count: 2, CPUs: 1, MemMiB: 4096, DiskGiB: 20, ControlPlanes: 1, ControlPlaneMemMiB: 2048}
	if big.sizes()[0].MemMiB != 4096 {
		t.Error("the control-plane floor never shrinks a VM")
	}
	each := addVMsRequest{Each: []vmSize{{Name: "w1", Role: "worker", CPUs: 2, MemMiB: 1024, DiskGiB: 20}, {Name: "cp", Role: "controlplane", CPUs: 2, MemMiB: 3072, DiskGiB: 20}}}
	if got := each.sizes(); got[0].Name != "cp" || got[1].Name != "w1" || each.controlPlanes() != 1 || each.totalMem() != 4096 {
		t.Errorf("per-VM sizing must list control planes first: %+v", got)
	}
	if each.validate() != nil {
		t.Error("valid per-VM request refused")
	}
	small := addVMsRequest{Each: []vmSize{{Role: "controlplane", CPUs: 2, MemMiB: 1024, DiskGiB: 20}}}
	if small.validate() == nil {
		t.Error("a 1 GiB control plane must be refused")
	}
}
