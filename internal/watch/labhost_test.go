package watch

import (
	"bytes"
	"context"
	"testing"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/labhost"
	"github.com/mikael/kubit/internal/store"
)

func labWatcher(t *testing.T) (*Watcher, *store.Machine) {
	t.Helper()
	c, _ := store.NewCrypto(bytes.Repeat([]byte{3}, 32))
	dir := t.TempDir()
	s, err := store.Open(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	host := &store.Machine{MAC: "aa:bb:cc:dd:ee:01", LabHost: &store.LabHost{State: "ready", Capacity: labhost.Capacity{Hostname: "lab-1"}}}
	return New(cluster.NewManager(s, dir), 0), host
}

func kindsOf(evs []store.EventRow) []string {
	var out []string
	for _, e := range evs {
		out = append(out, e.Severity+":"+e.Kind)
	}
	return out
}

func disk(pct int64) labhost.Metrics {
	return labhost.Metrics{DiskUsed: pct, DiskTotal: 100, MemTotal: 100}
}

// Disk: warns at 85, escalates once at 95, stays quiet in between, clears below 80.
func TestLabDiskThresholds(t *testing.T) {
	w, host := labWatcher(t)
	ctx := context.Background()
	key := store.LabHostKey(host.MAC)
	step := func(pct int64) []string {
		evs := w.labResourceEvents(ctx, host, disk(pct))
		w.emit(ctx, key, evs)
		return kindsOf(evs)
	}
	if got := step(84); len(got) != 0 {
		t.Errorf("84%%: %v", got)
	}
	if got := step(85); len(got) != 1 || got[0] != "warn:labhost.disk-low" {
		t.Errorf("85%%: %v", got)
	}
	if got := step(90); len(got) != 0 {
		t.Errorf("90%% while warned: %v", got)
	}
	if got := step(95); len(got) != 1 || got[0] != "critical:labhost.disk-low" {
		t.Errorf("95%%: %v", got)
	}
	if got := step(96); len(got) != 0 {
		t.Errorf("96%% while critical: %v", got)
	}
	if got := step(82); len(got) != 0 {
		t.Errorf("82%% is inside the hysteresis band: %v", got)
	}
	if got := step(79); len(got) != 1 || got[0] != "info:labhost.disk-ok" {
		t.Errorf("79%%: %v", got)
	}
	if w.Store.HasOpenEvent(ctx, key, "", "labhost.disk-low") {
		t.Error("disk-ok must close the alert")
	}
	if got := step(85); len(got) != 1 {
		t.Errorf("warn again after clearing: %v", got)
	}
}

// Memory: three consecutive readings over the line, one alert, cleared below 85.
func TestLabMemoryPressure(t *testing.T) {
	w, host := labWatcher(t)
	ctx := context.Background()
	key := store.LabHostKey(host.MAC)
	mem := func(pct int64) labhost.Metrics { return labhost.Metrics{MemUsed: pct, MemTotal: 100, DiskTotal: 100} }
	step := func(pct int64) []string {
		evs := w.labResourceEvents(ctx, host, mem(pct))
		w.emit(ctx, key, evs)
		return kindsOf(evs)
	}
	for i, pct := range []int64{95, 95} {
		if got := step(pct); len(got) != 0 {
			t.Errorf("reading %d: %v", i+1, got)
		}
	}
	if got := step(93); len(got) != 1 || got[0] != "warn:labhost.memory-pressure" {
		t.Errorf("third reading: %v", got)
	}
	if got := step(94); len(got) != 0 {
		t.Errorf("still high: %v", got)
	}
	if got := step(90); len(got) != 0 {
		t.Errorf("90%% is inside the band: %v", got)
	}
	if got := step(70); len(got) != 1 || got[0] != "info:labhost.memory-ok" {
		t.Errorf("recovery: %v", got)
	}
	if got := step(95); len(got) != 0 {
		t.Errorf("one high reading after recovery must not alert: %v", got)
	}
}

// Unreachable: the third failed tick alerts, the next good tick clears it.
func TestLabUnreachable(t *testing.T) {
	w, host := labWatcher(t)
	ctx := context.Background()
	key := store.LabHostKey(host.MAC)
	_ = w.Store.UpsertNode(ctx, store.NodeRow{MAC: host.MAC, IP: "10.0.0.9", Source: "labhost", State: "labhost"})
	_ = w.Store.SetLabHost(ctx, host.MAC, host.LabHost)
	var got []store.EventRow
	w.OnEvent = func(e store.EventRow) { got = append(got, e) }
	for i := 0; i < 3; i++ {
		w.labFailed(ctx, host, context.DeadlineExceeded)
	}
	if len(got) != 1 || got[0].Kind != "labhost.unreachable" || got[0].Severity != "critical" {
		t.Fatalf("after three failures: %v", kindsOf(got))
	}
	w.labFailed(ctx, host, context.DeadlineExceeded)
	if len(got) != 1 {
		t.Errorf("a fourth failure must not re-alert: %v", kindsOf(got))
	}
	if !w.Store.HasOpenEvent(ctx, key, "", "labhost.unreachable") {
		t.Error("alert must be open")
	}
	w.emit(ctx, key, []store.EventRow{{Cluster: key, Severity: "info", Kind: "labhost.back", Message: "back"}})
	if w.Store.HasOpenEvent(ctx, key, "", "labhost.unreachable") {
		t.Error("labhost.back must resolve the alert")
	}
}

type macLab struct {
	vms []labhost.VM
}

func (d *macLab) Capacity(context.Context) (labhost.Capacity, error) {
	return labhost.Capacity{Hostname: "mbp", MemMiB: 24576}, nil
}
func (d *macLab) EnsureTalosBoot(context.Context, string, string, string, string) (labhost.Boot, error) {
	return labhost.Boot{}, nil
}
func (d *macLab) Define(context.Context, labhost.VMSpec) error                     { return nil }
func (d *macLab) Start(context.Context, string) error                              { return nil }
func (d *macLab) Stop(context.Context, string, bool) error                         { return nil }
func (d *macLab) Delete(context.Context, string) error                             { return nil }
func (d *macLab) Resize(context.Context, string, int, int) error                   { return nil }
func (d *macLab) List(context.Context) ([]labhost.VM, error)                       { return d.vms, nil }
func (d *macLab) SetDiskBoot(context.Context, string) error                        { return nil }
func (d *macLab) SetTalosBoot(context.Context, string, labhost.Boot, string) error { return nil }
func (d *macLab) Close() error                                                     { return nil }
func (d *macLab) Metrics(context.Context) (labhost.Metrics, error) {
	return labhost.Metrics{MemTotal: 100, MemUsed: 10, DiskTotal: 100, DiskUsed: 10}, nil
}

func TestLabTickOnThisMac(t *testing.T) {
	w, _ := labWatcher(t)
	ctx := t.Context()
	d := &macLab{vms: []labhost.VM{{Name: "vm-01", MAC: "52:54:00:6b:01:01", State: "running", MemMiB: 3072}}}
	w.Manager.Local = func() (labhost.Driver, error) { return d, nil }
	mac := "84:2f:57:45:7e:dc"
	if err := w.Store.UpsertNode(ctx, store.NodeRow{MAC: mac, IP: "192.168.105.1", Source: "labhost", State: "labhost"}); err != nil {
		t.Fatal(err)
	}
	if err := w.Store.SetLabHost(ctx, mac, &store.LabHost{State: "ready", Driver: labhost.DriverVFKit}); err != nil {
		t.Fatal(err)
	}
	host, _ := w.Store.GetMachine(ctx, mac)
	w.labTick(ctx, host)
	got, _ := w.Store.GetMachine(ctx, mac)
	lh := got.LabHost
	if len(lh.VMs) != 1 || lh.Capacity.Hostname != "mbp" || lh.Metrics == nil || lh.Failures != 0 {
		t.Errorf("tick: %+v", lh)
	}
	if lh.Updates != nil {
		t.Error("a Mac has no package updates to check")
	}
}
