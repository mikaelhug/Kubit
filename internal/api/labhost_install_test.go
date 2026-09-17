package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/pxe"
	"github.com/mikael/kubit/internal/store"
)

// fakePXE serves a status.json the test mutates as the "machine" progresses.
type fakePXE struct {
	mu sync.Mutex
	st pxe.Status
}

func (f *fakePXE) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_ = json.NewEncoder(w).Encode(f.st)
}

func (f *fakePXE) set(fn func(*pxe.Status)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(&f.st)
}

func TestLabWaitBootPhases(t *testing.T) {
	c, _ := store.NewCrypto(bytes.Repeat([]byte{8}, 32))
	dir := t.TempDir()
	st, err := store.Open(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := New("test", cluster.NewManager(st, dir), "", c)
	ctx := context.Background()
	fake := &fakePXE{st: pxe.Status{Boots: []pxe.Boot{}}}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	v, _ := st.GetSettings(ctx)
	v.PXEStatusURL = srv.URL + "/status.json"
	if err := st.PutSettings(ctx, v); err != nil {
		t.Fatal(err)
	}
	labBootWait, labIPXEWait, labPollEvery = 300*time.Millisecond, 300*time.Millisecond, 50*time.Millisecond
	mac := "04:0e:3c:c5:4b:d1"
	var logs []string
	sink := func(e clusterEvent) {
		if e.Kind == "log" {
			logs = append(logs, e.Message)
		}
	}

	// Nothing ever asks to boot: the boot phase names the BIOS/LAN, not SSH.
	err = s.labWaitBoot(ctx, newPXEWatch(s, mac, sink))
	if err == nil || !strings.Contains(err.Error(), "no network boot request from "+mac) || !strings.Contains(err.Error(), "BIOS boot order") {
		t.Fatalf("boot phase error: %v", err)
	}

	// DHCP seen but the kernel never fetched: the transport is blamed. Only lines and
	// boots newer than the watch count, so the watch is primed before they appear.
	w0 := newPXEWatch(s, mac, sink)
	w0.poll(ctx)
	fake.set(func(p *pxe.Status) {
		p.Boots = []pxe.Boot{{MAC: mac, Arch: "amd64", Stage: "dhcp", LastSeen: time.Now()}}
		p.Log = []string{"12:00:00 PXE request from " + mac + " (amd64)"}
	})
	err = s.labWaitBoot(ctx, w0)
	if err == nil || !strings.Contains(err.Error(), "kernel was never fetched") {
		t.Fatalf("ipxe phase error: %v", err)
	}
	if len(logs) == 0 || !strings.Contains(logs[0], "pxe: 12:00:00 PXE request from "+mac) {
		t.Errorf("PXE log lines about the MAC must be mirrored into the operation: %v", logs)
	}

	// Kernel fetched: both phases pass and the IP is learned for the SSH phase.
	w := newPXEWatch(s, mac, sink)
	fake.set(func(p *pxe.Status) {
		p.Boots[0].Stage = "kernel"
		p.Boots[0].IP = "192.168.5.204"
		p.Boots[0].LastSeen = time.Now()
	})
	if err := s.labWaitBoot(ctx, w); err != nil {
		t.Fatalf("healthy boot: %v", err)
	}
	if w.ip != "192.168.5.204" {
		t.Errorf("watch must learn the IP from the PXE server, got %q", w.ip)
	}

	// A boot left over from an earlier attempt does not count for a new watch.
	fake.set(func(p *pxe.Status) { p.Boots[0].LastSeen = time.Now().Add(-time.Hour) })
	if err := s.labWaitBoot(ctx, newPXEWatch(s, mac, sink)); err == nil || !strings.Contains(err.Error(), "no network boot request") {
		t.Errorf("stale boot must not satisfy the boot phase: %v", err)
	}
}

func TestLabProgressRoute(t *testing.T) {
	c, _ := store.NewCrypto(bytes.Repeat([]byte{8}, 32))
	dir := t.TempDir()
	st, err := store.Open(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := New("test", cluster.NewManager(st, dir), "", c)
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
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/labhost/progress?mac="+mac+"&stage=installer", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("progress for a machine that is not a lab host: %d", rec.Code)
	}
	_ = st.SetLabHost(ctx, mac, &store.LabHost{State: "installing"})
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/labhost/progress?mac="+mac+"&stage=packages", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("progress: %d %s", rec.Code, rec.Body.String())
	}
	m, _ = st.GetMachine(ctx, mac)
	if m.LabHost == nil || m.LabHost.Install == nil || m.LabHost.Install.Stage != "packages" {
		t.Errorf("stage not recorded: %+v", m.LabHost)
	}
	// The preseed for an arm64 machine installs the arm emulator and reports progress.
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/labhost/preseed?mac="+mac+"&post=http://192.168.105.1:8069/labhost/"+mac+"/postinstall", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "qemu-system-arm") || !strings.Contains(rec.Body.String(), "http://192.168.105.1:8069/labhost/"+mac+"/progress?stage=installer") {
		t.Errorf("preseed: %d %s", rec.Code, rec.Body.String()[:200])
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
