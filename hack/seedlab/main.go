package main

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"math/rand"
	"os"
	"time"

	"github.com/mikael/kubit/internal/labhost"
	"github.com/mikael/kubit/internal/oob"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
)

func main() {
	dir := os.Getenv("KUBIT_HOME")
	if dir == "" {
		log.Fatal("seedlab writes demo data: set KUBIT_HOME to a scratch home")
	}
	crypto, err := store.LoadCrypto()
	if err != nil {
		log.Fatal(err)
	}
	s, err := store.Open(dir, crypto)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	mac := "02:00:00:00:00:01"
	if err := s.UpsertNode(ctx, store.NodeRow{MAC: mac, IP: "192.0.2.10", Source: "labhost", State: "labhost", Hostname: "lab-demo", Arch: "amd64", Serial: "DEMO000001"}); err != nil {
		log.Fatal(err)
	}
	now := time.Now()
	vms := []labhost.VM{}
	placed := [][]labhost.VMDisk{
		{{Target: "vda", Pool: "pool1", GiB: 60}},
		{{Target: "vda", Pool: "pool1", GiB: 60}, {Target: "vdb", Pool: "pool1", GiB: 200}},
		{{Target: "vda", Pool: labhost.SystemPool, GiB: 20}},
		{{Target: "vda", Pool: "pool1", GiB: 60}, {Target: "vdb", Device: "/dev/disk/by-id/wwn-0x5000c500a1b2c3d4", GiB: 3726}},
	}
	for i := 1; i <= 4; i++ {
		vm := labhost.VM{Name: "vm-0" + string(rune('0'+i)), MAC: labhost.MAC(1, i), State: "running", CPUs: 2, MemMiB: 3072, DiskGiB: placed[i-1][0].GiB, Boot: "disk", IP: "192.0.2.2" + string(rune('0'+i)), Disks: placed[i-1]}
		if len(placed[i-1]) > 1 {
			vm.DataGiB = placed[i-1][1].GiB
		}
		vms = append(vms, vm)
		_ = s.UpsertNode(ctx, store.NodeRow{MAC: vm.MAC, IP: vm.IP, Source: "lab", State: "maintenance", Arch: "amd64", Hostname: vm.Name})
		_ = s.SetMachineHost(ctx, vm.MAC, mac)
	}
	m := labhost.Metrics{Load1: 1.42, CPUPct: 37, MemUsed: 13314400000, MemTotal: 16 * (1 << 30), DiskUsed: 412 * (1 << 30), DiskTotal: 465 * (1 << 30), VMsRunning: 4, UptimeSec: 3*86400 + 5*3600, At: now.UTC().Format(time.RFC3339),
		Pools: []labhost.PoolUsage{{Name: labhost.SystemPool, Used: 31 * (1 << 30), Total: 210 * (1 << 30), Mounted: true}, {Name: "pool1", Used: 1700 * (1 << 30), Total: 1832 * (1 << 30), Mounted: true}}}
	disk := func(key, dev, model, use, pool string, gib uint64) labhost.HostDisk {
		return labhost.HostDisk{DiskRef: labhost.DiskRef{Key: key, DevPath: dev, Links: []string{key}, SizeBytes: gib << 30, Model: model}, ID: "/dev/disk/by-id/" + key, Transport: "sas", Use: use, Pool: pool}
	}
	storage := labhost.Capacity{
		Disks: []labhost.HostDisk{
			disk("wwn-0x600508b1001c4d5e", "/dev/sda", "LOGICAL VOLUME", labhost.DiskOS, "", 223),
			disk("wwn-0x600508b1001c4d5f", "/dev/sdb", "LOGICAL VOLUME", labhost.DiskPool, "pool1", 1862),
			disk("wwn-0x5000c500a1b2c3d4", "/dev/sdc", "ST4000NM0023", labhost.DiskFree, "", 3726),
			disk("wwn-0x5000c500a1b2c3d5", "/dev/sdd", "ST4000NM0023", labhost.DiskFree, "", 3726),
		},
		Pools: []labhost.Pool{
			{Name: labhost.SystemPool, Dir: "/var/lib/kubit/vms", Disk: "wwn-0x600508b1001c4d5e", Mounted: true, SizeBytes: 210 << 30, FreeBytes: 179 << 30},
			{Name: "pool1", Dir: "/var/lib/kubit/pools/pool1", Disk: "wwn-0x600508b1001c4d5f", Mounted: true, SizeBytes: 1832 << 30, FreeBytes: 132 << 30},
		},
	}
	u := labhost.Updates{Count: 12, Security: 3, RebootRequired: false, KernelRunning: "6.12.30-amd64", KernelInstalled: "6.12.32-amd64", Release: "Debian GNU/Linux 13 (trixie)", Unattended: true, CheckedAt: now.Add(-20 * time.Minute).UTC().Format(time.RFC3339)}
	lh := &store.LabHost{State: "ready", Capacity: labhost.Capacity{CPUs: 6, MemMiB: 16384, DiskGiB: 53, KVM: true, Kernel: "6.12.30-amd64", Libvirt: "10.10.0", Hostname: "lab-demo", Arch: "amd64", Bridge: "br0", Ready: true, CheckedAt: now.UTC().Format(time.RFC3339), Disks: storage.Disks, Pools: storage.Pools}, Talos: "v1.14.2", Index: 1, VMs: vms, Metrics: &m, Updates: &u}
	if err := s.SetLabHost(ctx, mac, lh); err != nil {
		log.Fatal(err)
	}
	key := store.LabHostKey(mac)
	for t := now.Add(-24 * time.Hour); t.Before(now); t = t.Add(time.Minute) {
		age := now.Sub(t).Hours()
		cpu := 30 + 15*math.Sin(age/2) + rand.Float64()*8
		disk := m.DiskTotal - int64(float64(53*(1<<30))*(1+age/24))
		if age > 6 && age < 6.5 {
			continue
		}
		_ = s.AddSamples(ctx, key, t, []store.Sample{{CPUMilli: int64(cpu * 10), CPUCap: 1000, MemBytes: m.MemUsed - int64(rand.Float64()*(1<<30)), MemCap: m.MemTotal, Pods: 4, Ready: true, Reachable: true, Disk: disk, DiskCap: m.DiskTotal}})
	}
	_, _ = s.AddEvent(ctx, store.EventRow{Cluster: key, Node: "pool1", Severity: "warn", Kind: "labhost.disk-low", Message: "lab-demo: pool1 (sdb) 92% full (1.7 TiB of 1.8 TiB)"})
	server := "02:00:00:00:00:02"
	hw, _ := json.Marshal(talos.Inventory{CPUs: 16, MemoryBytes: 64 << 30, Arch: "amd64", Manufacturer: "HP", Product: "ProLiant ML350p Gen8", Disks: []talos.Disk{
		{DevPath: "/dev/sda", SizeBytes: 240 << 30, Model: "LOGICAL VOLUME", Transport: "sas", Links: []string{"wwn-0x600508b1001c0001"}},
		{DevPath: "/dev/sdb", SizeBytes: 1800 << 30, Model: "LOGICAL VOLUME", Transport: "sas", Links: []string{"wwn-0x600508b1001c0002"}},
		{DevPath: "/dev/sdc", SizeBytes: 3600 << 30, Model: "LOGICAL VOLUME", Transport: "sas", Links: []string{"wwn-0x600508b1001c0003"}},
		{DevPath: "/dev/sdd", SizeBytes: 8 << 30, Model: "SD Card", Transport: "usb"},
	}})
	_ = s.UpsertNode(ctx, store.NodeRow{MAC: server, IP: "192.0.2.30", Source: "redfish", State: "maintenance", Hostname: "ml350", Arch: "amd64", Hardware: hw})
	_ = s.SetMachineOOB(ctx, server, &oob.Config{Type: "redfish", Host: "192.0.2.31", User: "admin", Password: "demo"})
	single := "02:00:00:00:00:04"
	one, _ := json.Marshal(talos.Inventory{CPUs: 32, MemoryBytes: 126 << 30, Arch: "amd64", Manufacturer: "HP", Product: "ProLiant ML350p Gen8", Disks: []talos.Disk{
		{DevPath: "/dev/sda", SizeBytes: 128_000_000_000, Model: "LOGICAL VOLUME", Transport: "sas", Rotational: true, Links: []string{"wwn-0x600508b1001ca5c8"}},
		{DevPath: "/dev/sr0", SizeBytes: 0, Model: "DVDROM", Transport: "sata", CDROM: true, Readonly: true},
	}})
	_ = s.UpsertNode(ctx, store.NodeRow{MAC: single, IP: "192.0.2.50", Source: "redfish", State: "maintenance", Hostname: "ml350-one", Arch: "amd64", Hardware: one})
	_ = s.SetMachineOOB(ctx, single, &oob.Config{Type: "redfish", Host: "192.0.2.51", User: "admin", Password: "demo"})
	bare := "02:00:00:00:00:03"
	_ = s.UpsertNode(ctx, store.NodeRow{MAC: bare, IP: "192.0.2.40", Source: "redfish", State: "off", Hostname: "dl380"})
	_ = s.SetMachineOOB(ctx, bare, &oob.Config{Type: "redfish", Host: "192.0.2.41", User: "admin", Password: "demo"})
	_, _ = s.AddEvent(ctx, store.EventRow{Cluster: key, Severity: "info", Kind: "labhost.updates", Message: "lab-demo: 12 package updates pending, reboot required"})
	log.Println("seeded", mac)
}
