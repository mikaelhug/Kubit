package vfkit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mikael/kubit/internal/labhost"
)

type fakeMac struct {
	mu     sync.Mutex
	calls  []string
	jobs   map[string]job
	out    map[string]string
	fail   map[string]bool
	posted []string
	down   bool
	label  func(string) string
}

func (f *fakeMac) run(_ context.Context, name string, args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cmd := strings.Join(append([]string{filepath.Base(name)}, args...), " ")
	f.calls = append(f.calls, cmd)
	if f.fail[cmd] {
		return "", errors.New(cmd + ": failed")
	}
	if name == "launchctl" {
		switch args[0] {
		case "list":
			var b strings.Builder
			b.WriteString("PID\tStatus\tLabel\n100\t0\tcom.apple.other\n")
			for l, j := range f.jobs {
				pid := "-"
				if j.PID > 0 {
					pid = fmt.Sprint(j.PID)
				}
				fmt.Fprintf(&b, "%s\t%d\t%s\n", pid, j.Status, l)
			}
			return b.String(), nil
		case "bootstrap":
			f.jobs[f.label(filepath.Base(filepath.Dir(args[2])))] = job{PID: 4242}
			f.down = false
		case "bootout":
			delete(f.jobs, strings.TrimPrefix(args[1], "gui/501/"))
		}
		return "", nil
	}
	return f.out[cmd], nil
}

func newFake(t *testing.T) (*Host, *fakeMac) {
	t.Helper()
	f := &fakeMac{jobs: map[string]job{}, out: map[string]string{}, fail: map[string]bool{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Method == http.MethodPost {
			b, _ := io.ReadAll(r.Body)
			f.posted = append(f.posted, string(b))
			f.down = f.down || strings.Contains(string(b), "HardStop")
			return
		}
		if f.down {
			http.Error(w, "gone", http.StatusBadGateway)
			return
		}
		fmt.Fprint(w, `{"state":"VirtualMachineStateRunning"}`)
	}))
	t.Cleanup(srv.Close)
	dir := shortDir(t)
	run := filepath.Join(dir, "vmnet-run")
	if err := os.WriteFile(run, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	h := &Host{Dir: filepath.Join(dir, "vms"), Domain: "gui/501", Leases: filepath.Join(dir, "leases"), VFKit: "/opt/homebrew/bin/vfkit", VMNetRun: run, Run: f.run, HTTP: srv.Client(), REST: func(string) (string, *http.Client) { return srv.URL, srv.Client() }}
	f.label = h.label
	return h, f
}

func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "kv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDefineStartAndBootSwitch(t *testing.T) {
	h, f := newFake(t)
	ctx := context.Background()
	iso := filepath.Join(t.TempDir(), "metal-arm64.iso")
	os.WriteFile(iso, []byte("iso"), 0o644)
	if err := h.Define(ctx, labhost.VMSpec{Name: "vm-01", MAC: "52:54:00:6B:09:F3", CPUs: 2, MemMiB: 3072, DiskGiB: 20, DataGiB: 10, ISO: iso}); err != nil {
		t.Fatal(err)
	}
	sp, err := h.readSpec("vm-01")
	if err != nil || sp.Boot != "talos" || !sp.Run || sp.MAC != "52:54:00:6b:09:f3" {
		t.Fatalf("spec: %+v %v", sp, err)
	}
	if fi, err := os.Stat(h.file("vm-01", "disk.raw")); err != nil || fi.Size() != 20<<30 {
		t.Fatalf("disk.raw: %v", err)
	}
	if fi, err := os.Stat(h.file("vm-01", "data.raw")); err != nil || fi.Size() != 10<<30 {
		t.Fatalf("data.raw: %v", err)
	}
	pl := read(t, h.file("vm-01", "launchd.plist"))
	order := []string{h.VMNetRun, "<string>shared</string>", "<string>--</string>", "<string>/opt/homebrew/bin/vfkit</string>", "disk.raw", "data.raw", "virtio-net,fd=4,mac=52:54:00:6b:09:f3", "unix://" + h.sock("vm-01"), "usb-mass-storage,path=" + iso + ",readonly"}
	at := 0
	for _, want := range order {
		i := strings.Index(pl[at:], want)
		if i < 0 {
			t.Fatalf("plist missing %q after offset %d:\n%s", want, at, pl)
		}
		at += i
	}
	if !strings.Contains(pl, "<string>"+h.label("vm-01")+"</string>") || !strings.Contains(pl, "<key>RunAtLoad</key><true/>") {
		t.Error("plist label or RunAtLoad wrong")
	}
	want := []string{"launchctl list", "launchctl bootout gui/501/" + h.label("vm-01"), "launchctl bootstrap gui/501 " + h.file("vm-01", "launchd.plist")}
	if got := strings.Join(f.calls, "\n"); got != strings.Join(want, "\n") {
		t.Errorf("calls:\n%s", got)
	}
	if err := h.Define(ctx, labhost.VMSpec{Name: "vm-01", MAC: "52:54:00:6b:09:f4", CPUs: 1, MemMiB: 2048, DiskGiB: 8}); err == nil {
		t.Error("a second define of the same name must fail")
	}
	if err := h.SetDiskBoot(ctx, "vm-01"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(read(t, h.file("vm-01", "launchd.plist")), "usb-mass-storage") {
		t.Error("disk boot must drop the ISO")
	}
	if err := h.SetTalosBoot(ctx, "vm-01", labhost.Boot{}, "arm64"); err == nil {
		t.Error("Talos boot without an ISO must fail")
	}
	if err := h.SetTalosBoot(ctx, "vm-01", labhost.Boot{ISO: iso}, "arm64"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(h.file("vm-01", "efi-vars"), []byte("vars"), 0o644)
	d, _ := os.OpenFile(h.file("vm-01", "disk.raw"), os.O_WRONLY, 0)
	d.WriteAt([]byte("TALOS"), 0)
	d.Close()
	if err := h.Stop(ctx, "vm-01", true); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.jobs[h.label("vm-01")]; ok {
		t.Error("force stop must unload the job")
	}
	if !strings.Contains(strings.Join(f.posted, ","), "HardStop") {
		t.Errorf("force stop must hard-stop: %v", f.posted)
	}
	if err := h.Start(ctx, "vm-01"); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 5)
	d, _ = os.Open(h.file("vm-01", "disk.raw"))
	d.ReadAt(b, 0)
	d.Close()
	if string(b) == "TALOS" {
		t.Error("re-provision must wipe the disk")
	}
	if _, err := os.Stat(h.file("vm-01", "efi-vars")); !os.IsNotExist(err) {
		t.Error("re-provision must drop the EFI variables")
	}
	if sp, _ := h.readSpec("vm-01"); sp.Wipe || sp.Boot != "talos" {
		t.Errorf("after start: %+v", sp)
	}
	if !strings.Contains(read(t, h.file("vm-01", "launchd.plist")), "usb-mass-storage") {
		t.Error("re-provision must attach the ISO again")
	}
}

func TestStopResizeDeleteAndList(t *testing.T) {
	h, f := newFake(t)
	ctx := context.Background()
	for i, n := range []string{"vm-01", "vm-02", "vm-03"} {
		if err := h.Define(ctx, labhost.VMSpec{Name: n, MAC: labhost.MAC(9, 0xe0+i), CPUs: 2, MemMiB: 2048, DiskGiB: 8}); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.Stop(ctx, "vm-02", false); err != nil {
		t.Fatal(err)
	}
	if len(f.posted) != 1 || !strings.Contains(f.posted[0], `"Stop"`) {
		t.Errorf("graceful stop: %v", f.posted)
	}
	f.jobs[h.label("vm-02")] = job{}
	f.jobs[h.label("vm-03")] = job{Status: 1}
	os.WriteFile(h.Leases, []byte("{\n\tname=vm-01\n\tip_address=192.168.105.7\n\thw_address=1,52:54:0:6b:9:e0\n\tidentifier=1,52:54:0:6b:9:e0\n\tlease=0x6ab64172\n}\n"), 0o644)
	if err := h.Resize(ctx, "vm-01", 4, 4096); err != nil {
		t.Fatal(err)
	}
	vms, err := h.List(ctx)
	if err != nil || len(vms) != 3 {
		t.Fatalf("list: %v %v", vms, err)
	}
	got := map[string]labhost.VM{}
	for _, v := range vms {
		got[v.Name] = v
	}
	if v := got["vm-01"]; v.State != "running" || v.IP != "192.168.105.7" || v.CPUs != 4 || v.MemMiB != 4096 || v.Boot != "disk" {
		t.Errorf("vm-01: %+v", v)
	}
	if got["vm-02"].State != "shut off" || got["vm-03"].State != "crashed" {
		t.Errorf("states: %+v", got)
	}
	if sp, _ := h.readSpec("vm-02"); sp.Run {
		t.Error("a stopped VM must not autostart")
	}
	if err := h.Delete(ctx, "vm-03"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(h.vmDir("vm-03")); !os.IsNotExist(err) {
		t.Error("delete must remove the VM directory")
	}
	if err := h.Delete(ctx, "vm-09"); err != nil {
		t.Errorf("deleting an absent VM is not an error: %v", err)
	}
	if err := checkName(bootDir); err == nil {
		t.Error("the boot directory is not a VM name")
	}
}

func TestAutostart(t *testing.T) {
	h, f := newFake(t)
	ctx := context.Background()
	for i, n := range []string{"vm-01", "vm-02"} {
		if err := h.Define(ctx, labhost.VMSpec{Name: n, MAC: labhost.MAC(9, 0xe0+i), CPUs: 1, MemMiB: 2048, DiskGiB: 8}); err != nil {
			t.Fatal(err)
		}
	}
	h.Stop(ctx, "vm-02", true)
	f.jobs = map[string]job{}
	if err := h.Autostart(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.jobs[h.label("vm-01")]; !ok {
		t.Error("vm-01 was running and must come back")
	}
	if _, ok := f.jobs[h.label("vm-02")]; ok {
		t.Error("vm-02 was stopped and must stay stopped")
	}
}

func TestParseLeases(t *testing.T) {
	in := `{
	name=talos
	ip_address=192.168.64.4
	hw_address=1,52:54:0:6b:1:1
	lease=0x6ab60000
}
{
	name=talos
	ip_address=192.168.105.9
	hw_address=1,52:54:0:6b:1:1
	lease=0x6ab60001
}
{
	name=talos
	ip_address=192.168.105.20
	hw_address=1,52:54:0:6b:1:1
	lease=0x6ab60100
}
{
	name=lab1
	ip_address=192.168.105.19
	hw_address=ff,a7:b6:68:b8:0:1:0:1
	lease=0x6ab60200
}
`
	got := parseLeases(strings.NewReader(in))
	if len(got) != 1 || got["52:54:00:6b:01:01"] != "192.168.105.20" {
		t.Errorf("leases: %v", got)
	}
}

func TestParseLaunchctlList(t *testing.T) {
	p := "dev.kubit.vm.0a1b2c3d."
	got := parseLaunchctlList("PID\tStatus\tLabel\n123\t0\t"+p+"vm-01\n-\t0\t"+p+"vm-02\n-\t-9\t"+p+"vm-03\n77\t0\tdev.kubit.vm.ffffffff.vm-01\n55\t0\tcom.apple.x\n", p)
	if len(got) != 3 || got[p+"vm-01"].PID != 123 || got[p+"vm-02"].PID != 0 || got[p+"vm-03"].Status != -9 {
		t.Errorf("jobs: %+v", got)
	}
}

func TestCapacityAndProblems(t *testing.T) {
	h, f := newFake(t)
	ctx := context.Background()
	f.out["sysctl -n hw.ncpu hw.memsize hw.model"] = "12\n25769803776\nMac16,8\n"
	f.out["sysctl -n kern.hv_support"] = "1\n"
	f.out["scutil --get LocalHostName"] = "mbp\n"
	f.out["sw_vers -productVersion"] = "27.0\n"
	f.out["df -k "+h.Dir] = "Filesystem 1024-blocks Used Available Capacity iused ifree %iused Mounted on\n/dev/disk3s5 971350180 500000000 400000000 56% 1 1 0% /System/Volumes/Data\n"
	f.out["vfkit --version"] = "vfkit version: v0.6.4\n"
	c, err := h.Capacity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if c.CPUs != 12 || c.MemMiB != 24576 || c.Model != "Mac16,8" || !c.KVM || c.Hostname != "mbp" || c.OS != "macOS 27.0" || c.Hypervisor != "vfkit v0.6.4" || c.DiskGiB != 381 || c.ReserveMiB != 6144 || !c.Ready || c.Problem != "" {
		t.Errorf("capacity: %+v", c)
	}
	if c.Arch == "" || c.Reserve() != 6144 {
		t.Errorf("arch/reserve: %+v", c)
	}
	f.out["sw_vers -productVersion"] = "15.6\n"
	if c, _ := h.Capacity(ctx); c.Ready || !strings.Contains(c.Problem, "macOS 26") {
		t.Errorf("old macOS: %+v", c)
	}
	f.fail["sw_vers -productVersion"] = true
	if c, _ := h.Capacity(ctx); !c.Ready {
		t.Errorf("an unknown macOS version must not block: %+v", c)
	}
	delete(f.fail, "sw_vers -productVersion")
	f.out["sw_vers -productVersion"] = "27.0\n"
	f.fail["sysctl -n kern.hv_support"] = true
	if c, err := h.Capacity(ctx); err != nil || c.Ready || c.CPUs != 12 {
		t.Errorf("no hv_support reading: %+v %v", c, err)
	}
	delete(f.fail, "sysctl -n kern.hv_support")
	os.Remove(h.VMNetRun)
	if c, _ := h.Capacity(ctx); c.Ready || !strings.Contains(c.Command, "vmnet-helper") {
		t.Errorf("no vmnet-helper: %+v", c)
	}
	f.fail["vfkit --version"] = true
	if c, _ := h.Capacity(ctx); c.Problem != "vfkit is not installed." || c.Command != "brew install vfkit" {
		t.Errorf("no vfkit: %+v", c)
	}
	f.out["sysctl -n hw.ncpu hw.memsize hw.model"] = "8\n8589934592\nMacmini9,1\n"
	if c, _ := h.Capacity(ctx); c.ReserveMiB != 4096 {
		t.Errorf("small Mac reserve: %d", c.ReserveMiB)
	}
}

func TestHostMAC(t *testing.T) {
	h, f := newFake(t)
	f.out["networksetup -getmacaddress en0"] = "Ethernet Address: 84:2f:57:45:7e:dc (Device: en0)\n"
	if m, err := h.HostMAC(context.Background()); err != nil || m != "84:2f:57:45:7e:dc" {
		t.Errorf("en0: %q %v", m, err)
	}
	f.fail["networksetup -getmacaddress en0"] = true
	f.out["networksetup -listallhardwareports"] = "Hardware Port: Thunderbolt Bridge\nDevice: bridge0\nEthernet Address: N/A\n\nHardware Port: Ethernet\nDevice: en7\nEthernet Address: a0:ce:c8:1:2:3\n"
	if m, err := h.HostMAC(context.Background()); err != nil || m != "a0:ce:c8:01:02:03" {
		t.Errorf("fallback: %q %v", m, err)
	}
}

func TestMetricsParsers(t *testing.T) {
	vmstat := `Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                               12345.
Pages active:                            100000.
Pages inactive:                           90000.
Pages wired down:                         50000.
Pages occupied by compressor:             10000.
`
	if got := parseVMStat(vmstat); got != 160000*16384 {
		t.Errorf("vm_stat: %d", got)
	}
	top := "Processes: 1\nCPU usage: 10.0% user, 5.0% sys, 85.0% idle\n\nProcesses: 1\nCPU usage: 20.5% user, 9.5% sys, 70.0% idle\n"
	if got := parseTopCPU(top); got != 30 {
		t.Errorf("top: %v", got)
	}
	now := time.Unix(1790000100, 0)
	load, up, mem := parseSysctl("{ 2.50 2.00 1.50 }\n{ sec = 1790000000, usec = 0 } Thu Sep 24 10:00:00 2026\n25769803776\n", now)
	if load != 2.5 || up != 100 || mem != 25769803776 {
		t.Errorf("sysctl: %v %v %v", load, up, mem)
	}
	total, avail := parseDF("Filesystem 1024-blocks Used Available\n/dev/disk3s5 1000 300 600 x\n")
	if total != 1000<<10 || avail != 600<<10 {
		t.Errorf("df: %d %d", total, avail)
	}
}

func TestEnsureTalosBoot(t *testing.T) {
	h, _ := newFake(t)
	var hits int
	body := "ISO"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if !strings.HasSuffix(r.URL.Path, "/image/0123456789abcdef/v1.14.0/metal-arm64.iso") {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, body)
	}))
	defer srv.Close()
	h.HTTP = srv.Client()
	ctx := context.Background()
	b, err := h.EnsureTalosBoot(ctx, srv.URL, "0123456789abcdef", "v1.14.0", "arm64")
	if err != nil || read(t, b.ISO) != "ISO" || b.Kernel != "" {
		t.Fatalf("boot: %+v %v", b, err)
	}
	if _, err := h.EnsureTalosBoot(ctx, srv.URL, "0123456789abcdef", "v1.14.0", "arm64"); err != nil || hits != 1 {
		t.Errorf("a present ISO must not be fetched again (%d requests)", hits)
	}
	body = ""
	if _, err := h.EnsureTalosBoot(ctx, srv.URL, "0123456789abcdef", "v1.15.0", "arm64"); err == nil {
		t.Error("a 404 must fail")
	}
	os.RemoveAll(filepath.Join(h.Dir, bootDir))
	if _, err := h.EnsureTalosBoot(ctx, srv.URL, "0123456789abcdef", "v1.14.0", "arm64"); err == nil {
		t.Error("an empty download must fail")
	}
	matches, _ := filepath.Glob(filepath.Join(h.Dir, bootDir, "*", "*"))
	if len(matches) != 0 {
		t.Errorf("a failed download must leave nothing behind: %v", matches)
	}
}

func TestNewRefusesOtherOS(t *testing.T) {
	defer func(g string) { goos = g }(goos)
	goos = "linux"
	if _, err := New(t.TempDir()); err == nil {
		t.Error("New must refuse off macOS")
	}
}

func TestDefineCleansUpWhenStartFails(t *testing.T) {
	h, f := newFake(t)
	f.fail["launchctl bootstrap gui/501 "+h.file("vm-05", "launchd.plist")] = true
	if err := h.Define(context.Background(), labhost.VMSpec{Name: "vm-05", MAC: labhost.MAC(9, 0xd5), CPUs: 1, MemMiB: 2048, DiskGiB: 8}); err == nil {
		t.Fatal("define must fail when the VM cannot start")
	}
	if _, err := os.Stat(h.vmDir("vm-05")); !os.IsNotExist(err) {
		t.Error("a VM that never started must leave nothing behind")
	}
}

func TestUnixREST(t *testing.T) {
	h, _ := newFake(t)
	if err := os.MkdirAll(h.vmDir("vm-01"), 0o700); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", h.sock("vm-01"))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.Path+" "+string(b))
		fmt.Fprint(w, `{"state":"VirtualMachineStateRunning"}`)
	})}
	go srv.Serve(l)
	defer srv.Close()
	h.REST = h.unixREST
	ctx := context.Background()
	if st, err := h.state(ctx, "vm-01"); err != nil || st != "VirtualMachineStateRunning" {
		t.Fatalf("state over the socket: %q %v", st, err)
	}
	if err := h.setState(ctx, "vm-01", "Stop"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1] != `POST /vm/state {"state":"Stop"}` {
		t.Errorf("requests: %q", got)
	}
	if err := h.setState(ctx, "vm-02", "Stop"); err == nil || !refused(err) {
		t.Errorf("a VM without a socket must read as not running: %v", err)
	}
}

func TestSocketPathLimit(t *testing.T) {
	h, f := newFake(t)
	h.Dir = filepath.Join(h.Dir, strings.Repeat("d", 90))
	if err := h.Define(context.Background(), labhost.VMSpec{Name: "vm-01", MAC: labhost.MAC(9, 0xc1), CPUs: 1, MemMiB: 2048, DiskGiB: 8}); err == nil || !strings.Contains(err.Error(), "socket") {
		t.Errorf("an over-long socket path must be refused: %v", err)
	}
	f.out["sysctl -n hw.ncpu hw.memsize hw.model"] = "8\n8589934592\nMacmini9,1\n"
	f.out["sysctl -n kern.hv_support"] = "1\n"
	f.out["sw_vers -productVersion"] = "27.0\n"
	f.out["vfkit --version"] = "vfkit version: v0.6.4\n"
	if c, _ := h.Capacity(context.Background()); c.Ready || !strings.Contains(c.Problem, "too long") {
		t.Errorf("capacity must flag the long path: %+v", c)
	}
}

func TestLabelsAreScopedToTheHome(t *testing.T) {
	a, b := &Host{Dir: "/Users/x/.kubit/vms"}, &Host{Dir: "/tmp/scratch/vms"}
	if a.label("vm-01") == b.label("vm-01") || !strings.HasPrefix(a.label("vm-01"), labelBase) || !strings.HasSuffix(a.label("vm-01"), ".vm-01") {
		t.Errorf("labels: %s %s", a.label("vm-01"), b.label("vm-01"))
	}
}
