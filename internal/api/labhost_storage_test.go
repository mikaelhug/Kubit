package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mikael/kubit/internal/labhost"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
)

func hostDisk(key, dev, use, pool string, gib uint64) labhost.HostDisk {
	return labhost.HostDisk{DiskRef: labhost.DiskRef{Key: key, DevPath: dev, SizeBytes: gib << 30}, ID: "/dev/disk/by-id/" + key, Use: use, Pool: pool}
}

func hpCapacity() labhost.Capacity {
	return labhost.Capacity{
		Disks: []labhost.HostDisk{
			hostDisk("wwn-a", "/dev/sda", labhost.DiskOS, "", 300),
			hostDisk("wwn-b", "/dev/sdb", labhost.DiskFree, "", 2000),
			hostDisk("wwn-c", "/dev/sdc", labhost.DiskPool, "pool1", 1000),
			hostDisk("wwn-d", "/dev/sdd", labhost.DiskPool, "pool2", 500),
			hostDisk("wwn-f", "/dev/sdf", labhost.DiskBusy, "", 500),
			hostDisk("wwn-g", "/dev/sdg", labhost.DiskFree, "", 900),
		},
		Pools: []labhost.Pool{
			{Name: labhost.SystemPool, Disk: "wwn-a", Mounted: true, SizeBytes: 300 << 30, FreeBytes: 200 << 30},
			{Name: "pool1", Disk: "wwn-c", Mounted: true, SizeBytes: 1000 << 30, FreeBytes: 900 << 30},
			{Name: "pool2", Disk: "wwn-d", SizeBytes: 0},
		},
	}
}

func TestPlanStoragePlacesImagesAndWholeDisks(t *testing.T) {
	capa := hpCapacity()
	vm := func(system, data string, dataGiB int) vmSize {
		return vmSize{CPUs: 2, MemMiB: 3072, DiskGiB: 60, DataGiB: dataGiB, SystemDisk: system, DataDisk: data}
	}
	layout, err := planStorage([]vmSize{vm("", "", 0), vm("image:debian", "", 20), vm("image:wwn-b", "image:wwn-b", 100), vm("image:wwn-b", "whole:wwn-g", 0)}, capa, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := layout.VMs
	if got[0].System.Pool != "pool1" || got[1].System.Pool != labhost.SystemPool || got[1].Data.Pool != labhost.SystemPool {
		t.Errorf("defaults go to the roomiest storage disk; a legacy data image sits next to its system image: %+v", got[:2])
	}
	if len(layout.Create) != 1 || layout.Create[0] != (poolSpec{Name: "pool3", Device: "/dev/disk/by-id/wwn-b", Disk: "wwn-b"}) || got[2].System.Pool != "pool3" || got[2].Data.Pool != "pool3" || got[3].System.Pool != "pool3" {
		t.Errorf("a free disk becomes one new storage disk: %+v %+v", layout.Create, got[2:])
	}
	if got[3].Data != (labhost.Placement{Device: "/dev/disk/by-id/wwn-g"}) {
		t.Errorf("whole data disk: %+v", got[3].Data)
	}
	big, err := planStorage([]vmSize{{CPUs: 1, MemMiB: 2048, DiskGiB: 400, SystemDisk: "image:debian"}}, capa, nil)
	if err != nil || len(big.Warnings) != 1 || !strings.Contains(big.Warnings[0], "Debian's disk may grow to 400 GiB") {
		t.Errorf("overcommit only warns: %v %v", err, big.Warnings)
	}
	empty, err := planStorage([]vmSize{vm("whole:wwn-c", "", 0)}, capa, nil)
	if err != nil || len(empty.Release) != 1 || empty.Release[0] != "pool1" || empty.VMs[0].System.Device != "/dev/disk/by-id/wwn-c" {
		t.Errorf("an empty storage disk is released for a whole-disk VM: %v %+v", err, empty)
	}
	existing := []labhost.VM{{Name: "db", Disks: []labhost.VMDisk{{Target: "vda", Pool: "pool1", GiB: 60}, {Target: "vdb", Device: "/dev/disk/by-id/wwn-g"}}}}
	for _, c := range []struct {
		sizes []vmSize
		vms   []labhost.VM
		want  string
	}{
		{[]vmSize{vm("whole:wwn-a", "", 0)}, nil, "holds Debian"},
		{[]vmSize{vm("whole:debian", "", 0)}, nil, "cannot be used whole"},
		{[]vmSize{vm("whole:wwn-b", "", 0), vm("whole:wwn-b", "", 0)}, nil, "chosen twice"},
		{[]vmSize{vm("whole:wwn-b", "", 0), vm("image:wwn-b", "", 0)}, nil, "cannot hold images and be used whole"},
		{[]vmSize{vm("image:wwn-b", "", 0), vm("whole:wwn-b", "", 0)}, nil, "cannot hold images and be used whole"},
		{[]vmSize{vm("whole:wwn-c", "", 0)}, existing, "holds VM images"},
		{[]vmSize{vm("", "whole:wwn-g", 0)}, existing, "belongs to db"},
		{[]vmSize{vm("image:wwn-d", "", 0)}, nil, "not mounted"},
		{[]vmSize{vm("image:wwn-f", "", 0)}, nil, "in use on the host"},
		{[]vmSize{vm("image:wwn-x", "", 0)}, nil, "not on this host"},
	} {
		if _, err := planStorage(c.sizes, capa, c.vms); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v, want %q", c.sizes, err, c.want)
		}
	}
}

func TestTranslatePlanFindsTheDisksAfterInstall(t *testing.T) {
	planned := []labhost.DiskRef{
		{Key: "scsi-3a", DevPath: "/dev/sda", Links: []string{"scsi-3a"}, SizeBytes: 300 << 30},
		{Key: "wwid-naa.b", DevPath: "/dev/sdb", WWID: "naa.b", SizeBytes: 2000 << 30},
		{Key: "serial-gone", Serial: "gone"},
	}
	host := []labhost.HostDisk{
		{DiskRef: labhost.DiskRef{Key: "wwn-0xa", DevPath: "/dev/sdb", Links: []string{"scsi-3a", "wwn-0xa"}, SizeBytes: 300 << 30}, Use: labhost.DiskOS},
		{DiskRef: labhost.DiskRef{Key: "wwn-0xb", DevPath: "/dev/sda", WWID: "naa.b", SizeBytes: 2000 << 30}, Use: labhost.DiskFree},
	}
	req := addVMsRequest{Each: []vmSize{{SystemDisk: "image:scsi-3a", DataDisk: "whole:wwid-naa.b"}, {SystemDisk: "image:debian"}}}
	out, err := translatePlan(req, planned, host)
	if err != nil {
		t.Fatal(err)
	}
	if out.Each[0].SystemDisk != "image:debian" || out.Each[0].DataDisk != "whole:wwn-0xb" || out.Each[1].SystemDisk != "image:debian" || req.Each[0].SystemDisk != "image:scsi-3a" {
		t.Errorf("translated: %+v (request left alone: %+v)", out.Each, req.Each)
	}
	for _, each := range [][]vmSize{{{SystemDisk: "whole:scsi-3a"}}, {{DataDisk: "image:serial-gone", DataGiB: 1}}, {{SystemDisk: "image:never-planned"}}} {
		if _, err := translatePlan(addVMsRequest{Each: each}, planned, host); err == nil {
			t.Errorf("%+v must fail before any VM is defined", each)
		}
	}
}

func TestNextVMMACFillsGaps(t *testing.T) {
	used := map[string]bool{labhost.MAC(3, 2): true, labhost.MAC(3, 3): true}
	if m, err := nextVMMAC(3, used); err != nil || m != labhost.MAC(3, 1) {
		t.Errorf("first gap: %s %v", m, err)
	}
	used[labhost.MAC(3, 1)] = true
	if m, _ := nextVMMAC(3, used); m != labhost.MAC(3, 4) {
		t.Errorf("never a live MAC: %s", m)
	}
}

func TestLabPlanDisksNeedTheInventory(t *testing.T) {
	inv := talos.Inventory{Disks: []talos.Disk{
		{DevPath: "/dev/sda", SizeBytes: 300 << 30, Links: []string{"wwn-0xa"}},
		{DevPath: "/dev/sdb", SizeBytes: 2000 << 30, Links: []string{"wwn-0xb"}},
		{DevPath: "/dev/sdc", SizeBytes: 8 << 30, Transport: "usb"},
	}}
	hw, _ := json.Marshal(inv)
	m := &store.Machine{MAC: "aa", Hardware: hw}
	plan := labPlan{Disk: "wwn-0xa", VMs: &addVMsRequest{Each: []vmSize{{CPUs: 2, MemMiB: 3072, DiskGiB: 60, SystemDisk: "image:wwn-0xb", DataDisk: "whole:wwn-0xb"}}}}
	if err := labPlanDisks(m, &plan); err == nil || !strings.Contains(err.Error(), "cannot hold images and be used whole") {
		t.Errorf("choices are checked against the inventory: %v", err)
	}
	plan.VMs.Each[0].DataDisk = ""
	if err := labPlanDisks(m, &plan); err != nil || plan.install == nil || plan.install.DevPath != "/dev/sda" || len(plan.disks) != 2 {
		t.Errorf("plan: %v %+v %+v", err, plan.install, plan.disks)
	}
	plan.Disk = "/dev/sdb"
	if err := labPlanDisks(m, &plan); err != nil || plan.install.Key != "wwn-0xb" {
		t.Errorf("a device path from the inventory: %v %+v", err, plan.install)
	}
	plan.Disk = "/dev/sdc"
	if err := labPlanDisks(m, &plan); err == nil {
		t.Error("a USB disk is no install disk")
	}
	bare := labPlan{Disk: "/dev/nvme0n1", VMs: &addVMsRequest{Each: []vmSize{{CPUs: 2, MemMiB: 3072, DiskGiB: 60, SystemDisk: "image:debian"}}}}
	if err := labPlanDisks(&store.Machine{MAC: "bb"}, &bare); err == nil || !strings.Contains(err.Error(), "Scan the machine's disks first") {
		t.Errorf("disk choices without an inventory: %v", err)
	}
	bare.VMs = nil
	if err := labPlanDisks(&store.Machine{MAC: "bb"}, &bare); err != nil || bare.install.DevPath != "/dev/nvme0n1" {
		t.Errorf("a /dev path without an inventory still pins the install disk: %v", err)
	}
}

type poolDriver struct {
	*memDriver
	created []poolSpec
	removed []string
}

func (d *poolDriver) CreatePool(_ context.Context, name, device string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.created = append(d.created, poolSpec{Name: name, Device: device})
	d.capa.Pools = append(d.capa.Pools, labhost.Pool{Name: name, Mounted: true, SizeBytes: 2000 << 30, FreeBytes: 2000 << 30})
	return nil
}

func (d *poolDriver) RemovePool(_ context.Context, name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.removed = append(d.removed, name)
	return nil
}

func TestStorageDisksAreCreatedOnDemandAndReleased(t *testing.T) {
	s, st, md := localServer(t)
	md.capa = hpCapacity()
	md.capa.MemMiB, md.capa.CPUs = 65536, 16
	d := &poolDriver{memDriver: md}
	s.manager.Local = func() (labhost.Driver, error) { return d, nil }
	ctx := context.Background()
	mac := "52:54:00:aa:00:01"
	if err := st.UpsertNode(ctx, store.NodeRow{MAC: mac, IP: "192.168.1.40", Source: "labhost", State: "labhost"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetLabHost(ctx, mac, &store.LabHost{State: "ready", Driver: labhost.DriverVFKit, Index: 1, Capacity: md.capa}); err != nil {
		t.Fatal(err)
	}
	var log []string
	layout, err := s.applyStorage(ctx, d, mac, []vmSize{{CPUs: 2, MemMiB: 3072, DiskGiB: 60, SystemDisk: "image:wwn-b"}}, nil, func(f string, a ...any) { log = append(log, f) })
	if err != nil || len(d.created) != 1 || d.created[0] != (poolSpec{Name: "pool3", Device: "/dev/disk/by-id/wwn-b"}) || layout.VMs[0].System.Pool != "pool3" {
		t.Fatalf("on-demand storage disk: %v %+v %+v", err, d.created, layout)
	}
	if fresh, _ := st.GetMachine(ctx, mac); len(fresh.LabHost.Capacity.Pools) != 4 {
		t.Errorf("the host's capacity is refreshed after formatting: %+v", fresh.LabHost.Capacity.Pools)
	}
	if rec := call(t, s, "POST", "/api/v1/machines/"+mac+"/labhost/vms", `{"each":[{"cpus":2,"memMiB":3072,"diskGiB":60,"systemDisk":"image:wwn-b"}]}`); rec.Code != 400 || !strings.Contains(rec.Body.String(), "Debian lab host") {
		t.Errorf("VMs on this Mac take no disk choices: %d %s", rec.Code, rec.Body)
	}
	md.vms = []labhost.VM{{Name: "db", MAC: labhost.MAC(1, 1), Disks: []labhost.VMDisk{{Target: "vda", Pool: "pool1", GiB: 60}}}}
	if _, err := st.AddEvent(ctx, store.EventRow{Cluster: store.LabHostKey(mac), Node: "pool1", Severity: "warn", Kind: "labhost.disk-low", Message: "pool1 90% full"}); err != nil {
		t.Fatal(err)
	}
	if rec := call(t, s, "DELETE", "/api/v1/machines/"+mac+"/labhost/pools/pool1", ""); rec.Code != 409 || !strings.Contains(rec.Body.String(), "db") || len(d.removed) != 0 {
		t.Fatalf("a storage disk with images stays: %d %s", rec.Code, rec.Body)
	}
	md.vms = nil
	if rec := call(t, s, "DELETE", "/api/v1/machines/"+mac+"/labhost/pools/pool1", ""); rec.Code != 204 || len(d.removed) != 1 || d.removed[0] != "pool1" {
		t.Fatalf("release: %d %s %v", rec.Code, rec.Body, d.removed)
	}
	if st.HasOpenEvent(ctx, store.LabHostKey(mac), "pool1", "labhost.disk-low") {
		t.Error("releasing a storage disk closes its alert")
	}
	for path, want := range map[string]int{"/api/v1/machines/" + mac + "/labhost/pools/system": 400, "/api/v1/machines/" + mac + "/labhost/pools/pool9": 404} {
		if rec := call(t, s, "DELETE", path, ""); rec.Code != want {
			t.Errorf("%s: %d, want %d", path, rec.Code, want)
		}
	}
}
