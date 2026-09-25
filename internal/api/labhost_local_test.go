package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/labhost"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
)

type memDriver struct {
	mu      sync.Mutex
	capa    labhost.Capacity
	mac     string
	vms     []labhost.VM
	deleted []string
}

func (d *memDriver) Capacity(context.Context) (labhost.Capacity, error) { return d.capa, nil }
func (d *memDriver) EnsureTalosBoot(_ context.Context, _, schematic, version, arch string) (labhost.Boot, error) {
	return labhost.Boot{ISO: "/vms/boot/" + version + "-" + schematic[:12] + "/metal-" + arch + ".iso"}, nil
}
func (d *memDriver) Define(context.Context, labhost.VMSpec) error                     { return nil }
func (d *memDriver) Start(context.Context, string) error                              { return nil }
func (d *memDriver) Stop(context.Context, string, bool) error                         { return nil }
func (d *memDriver) Resize(context.Context, string, int, int) error                   { return nil }
func (d *memDriver) SetDiskBoot(context.Context, string) error                        { return nil }
func (d *memDriver) SetTalosBoot(context.Context, string, labhost.Boot, string) error { return nil }
func (d *memDriver) Metrics(context.Context) (labhost.Metrics, error)                 { return labhost.Metrics{}, nil }
func (d *memDriver) Close() error                                                     { return nil }
func (d *memDriver) HostMAC(context.Context) (string, error)                          { return d.mac, nil }
func (d *memDriver) List(context.Context) ([]labhost.VM, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]labhost.VM(nil), d.vms...), nil
}
func (d *memDriver) Delete(_ context.Context, name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.deleted = append(d.deleted, name)
	return nil
}

func localServer(t *testing.T) (*Server, *store.Store, *memDriver) {
	t.Helper()
	c, _ := store.NewCrypto(bytes.Repeat([]byte{11}, 32))
	dir := t.TempDir()
	st, err := store.Open(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	factory := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/versions":
			fmt.Fprint(w, `["v1.14.0","v1.14.1"]`)
		case "/schematics":
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"id":"d9ff89777e246792e7642abd3220a616afb4e49822382e4213a2e528ab826fe5"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(factory.Close)
	d := &memDriver{mac: "84:2f:57:45:7e:dc", capa: labhost.Capacity{CPUs: 12, MemMiB: 24576, DiskGiB: 300, KVM: true, Hostname: "mbp", Arch: "arm64", Model: "Mac16,8", OS: "macOS 27.0", Hypervisor: "vfkit v0.6.4", ReserveMiB: 6144, Ready: true}}
	m := cluster.NewManager(st, dir)
	m.Factory.BaseURL = factory.URL
	m.Local = func() (labhost.Driver, error) { return d, nil }
	return New("test", m, "", c), st, d
}

func call(t *testing.T, s *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:40000"
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func waitOp(t *testing.T, st *store.Store, id int64) *store.OperationRow {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		op, err := st.GetOperation(context.Background(), id)
		if err == nil && op.Status != "running" {
			return op
		}
		time.Sleep(20 * time.Millisecond)
	}
	op, _ := st.GetOperation(context.Background(), id)
	t.Fatalf("operation %d still running: %s", id, op.Log)
	return nil
}

func TestLabLocalCreate(t *testing.T) {
	s, st, d := localServer(t)
	ctx := t.Context()
	rec := call(t, s, "GET", "/api/v1/labhosts/local", "")
	var info struct {
		Supported bool             `json:"supported"`
		Capacity  labhost.Capacity `json:"capacity"`
		Host      string           `json:"host"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &info) != nil || !info.Supported || info.Capacity.ReserveMiB != 6144 || info.Host != "" {
		t.Fatalf("local: %d %s", rec.Code, rec.Body)
	}
	for body, want := range map[string]int{
		`{"driver":"libvirt"}`:                                                     400,
		`{"driver":"vfkit","manual":true}`:                                         400,
		`{"driver":"vfkit","network":"routed"}`:                                    400,
		`{"driver":"vfkit","vms":{"count":7,"cpus":2,"memMiB":3072,"diskGiB":20}}`: 422,
	} {
		if rec := call(t, s, "POST", "/api/v1/labhosts", body); rec.Code != want {
			t.Errorf("%s: %d, want %d (%s)", body, rec.Code, want, rec.Body)
		}
	}
	d.capa.Problem, d.capa.Command = "vfkit is not installed.", "brew install vfkit"
	rec = call(t, s, "POST", "/api/v1/labhosts", `{"driver":"vfkit"}`)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "vfkit-missing") || !strings.Contains(rec.Body.String(), "brew install vfkit") {
		t.Errorf("problem: %d %s", rec.Code, rec.Body)
	}
	d.capa.Problem, d.capa.Command = "", ""
	rec = call(t, s, "POST", "/api/v1/labhosts", `{"driver":"vfkit"}`)
	if rec.Code != 202 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		OperationID int64  `json:"operationId"`
		MAC         string `json:"mac"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	if op := waitOp(t, st, out.OperationID); op.Status != "done" || op.Kind != "labhost.local" {
		t.Fatalf("op: %s %s", op.Status, op.Log)
	}
	host, err := st.GetMachine(ctx, d.mac)
	if err != nil || out.MAC != d.mac {
		t.Fatal(err)
	}
	lh := host.LabHost
	if host.IP != "192.168.105.1" || host.Kind() != store.KindLabHost || lh == nil || lh.Driver != labhost.DriverVFKit || lh.State != "ready" || !strings.HasSuffix(lh.ISO, "metal-arm64.iso") || lh.Kernel != "" || lh.Index == 0 {
		t.Fatalf("host: %+v %+v", host, lh)
	}
	if rec := call(t, s, "POST", "/api/v1/labhosts", `{"driver":"vfkit"}`); rec.Code != 409 {
		t.Errorf("second create: %d", rec.Code)
	}
	for _, p := range []string{"check", "update", "reboot"} {
		if rec := call(t, s, "POST", "/api/v1/machines/"+d.mac+"/labhost/"+p, ""); rec.Code != 409 {
			t.Errorf("%s on this Mac: %d", p, rec.Code)
		}
	}
	if rec := call(t, s, "POST", "/api/v1/machines/"+d.mac+"/labhost", `{"manual":true}`); rec.Code != 409 {
		t.Errorf("make lab host on this Mac: %d", rec.Code)
	}
	_ = st.UpdateLabHost(ctx, d.mac, func(l *store.LabHost) { l.State = "error" })
	if rec := call(t, s, "POST", "/api/v1/machines/"+d.mac+"/labhost", `{"manual":true}`); rec.Code != 409 {
		t.Errorf("make lab host on this Mac in error: %d", rec.Code)
	}
	_ = st.UpdateLabHost(ctx, d.mac, func(l *store.LabHost) { l.State = "ready" })
	fresh, _ := st.GetMachine(ctx, d.mac)
	if _, err := s.labSetup(ctx, d, fresh, nil); err != nil {
		t.Fatal(err)
	}
	if again, _ := st.GetMachine(ctx, d.mac); again.LabHost.Driver != labhost.DriverVFKit {
		t.Error("setting up again must keep the driver")
	}
}

func TestLabLocalDesignAndRelease(t *testing.T) {
	s, st, d := localServer(t)
	ctx := t.Context()
	v, _ := st.GetSettings(ctx)
	v.DefaultMetalLB = "10.0.0.200-10.0.0.220"
	if err := st.PutSettings(ctx, v); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertNode(ctx, store.NodeRow{MAC: d.mac, IP: "192.168.105.1", Source: "labhost", State: "labhost"}); err != nil {
		t.Fatal(err)
	}
	var macs []string
	for i := 1; i <= 3; i++ {
		mac := labhost.MAC(1, i)
		hw, _ := json.Marshal(talos.Inventory{CPUs: 2, MemoryBytes: 3 << 30, Arch: "arm64", Disks: []talos.Disk{{DevPath: "/dev/vda", SizeBytes: 20 << 30}}})
		if err := st.UpsertNode(ctx, store.NodeRow{IP: fmt.Sprintf("192.168.105.%d", 20+i), MAC: mac, Arch: "arm64", State: "maintenance", Source: "lab", Hardware: hw}); err != nil {
			t.Fatal(err)
		}
		_ = st.SetMachineHost(ctx, mac, d.mac)
		macs = append(macs, mac)
		d.vms = append(d.vms, labhost.VM{Name: fmt.Sprintf("vm-%02d", i), MAC: mac})
	}
	if err := st.SetLabHost(ctx, d.mac, &store.LabHost{State: "ready", Driver: labhost.DriverVFKit, Index: 1, VMs: d.vms[:2]}); err != nil {
		t.Fatal(err)
	}
	c, err := s.labDesign(ctx, "mac", macs, map[string]bool{macs[0]: true, macs[1]: true, macs[2]: true})
	if err != nil {
		t.Fatal(err)
	}
	if c.Spec.Platform.MetalLB.Range != "192.168.105.200-192.168.105.220" || c.Spec.ControlPlane.VIP != "192.168.105.250" {
		t.Errorf("addresses must sit on the Mac's VM network: range %q vip %q", c.Spec.Platform.MetalLB.Range, c.Spec.ControlPlane.VIP)
	}
	host, _ := st.GetMachine(ctx, d.mac)
	s.releaseLabHost(ctx, host)
	if _, err := st.GetMachine(ctx, d.mac); err == nil {
		t.Error("releasing this Mac must remove its row")
	}
	if strings.Join(d.deleted, ",") != "vm-01,vm-02,vm-03" {
		t.Errorf("release must delete every VM on the Mac, recorded or not: %v", d.deleted)
	}
	for _, mac := range macs {
		if _, err := st.GetMachine(ctx, mac); err == nil {
			t.Errorf("VM row %s must go with the host", mac)
		}
	}
}
