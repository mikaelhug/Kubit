// seedlab fills a Kubit home with one fake lab host (metrics, history, updates and
// alerts) so the Lab host tab can be reviewed without hardware. Dev only.
package main

import (
	"context"
	"log"
	"math"
	"math/rand"
	"os"
	"time"

	"github.com/mikael/kubit/internal/labhost"
	"github.com/mikael/kubit/internal/store"
)

func main() {
	dir := os.Getenv("KUBIT_HOME")
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
	mac := "04:0e:3c:c5:4b:d1"
	if err := s.UpsertNode(ctx, store.NodeRow{MAC: mac, IP: "192.168.5.204", Source: "labhost", State: "labhost", Hostname: "lab-8cc9490gvs", Arch: "amd64", Serial: "8CC9490GVS"}); err != nil {
		log.Fatal(err)
	}
	now := time.Now()
	vms := []labhost.VM{}
	for i := 1; i <= 4; i++ {
		vm := labhost.VM{Name: "vm-0" + string(rune('0'+i)), MAC: labhost.MAC(1, i), State: "running", CPUs: 2, MemMiB: 3072, DiskGiB: 20, Boot: "disk", IP: "192.168.5.21" + string(rune('0'+i))}
		vms = append(vms, vm)
		_ = s.UpsertNode(ctx, store.NodeRow{MAC: vm.MAC, IP: vm.IP, Source: "lab", State: "maintenance", Arch: "amd64", Hostname: vm.Name})
		_ = s.SetMachineHost(ctx, vm.MAC, mac)
	}
	m := labhost.Metrics{Load1: 1.42, CPUPct: 37, MemUsed: 13314400000, MemTotal: 16 * (1 << 30), DiskUsed: 412 * (1 << 30), DiskTotal: 465 * (1 << 30), VMsRunning: 4, UptimeSec: 3*86400 + 5*3600, At: now.UTC().Format(time.RFC3339)}
	u := labhost.Updates{Count: 12, Security: 3, RebootRequired: false, KernelRunning: "6.12.30-amd64", KernelInstalled: "6.12.32-amd64", Release: "Debian GNU/Linux 13 (trixie)", Unattended: true, CheckedAt: now.Add(-20 * time.Minute).UTC().Format(time.RFC3339)}
	lh := &store.LabHost{State: "ready", Capacity: labhost.Capacity{CPUs: 6, MemMiB: 16384, DiskGiB: 53, KVM: true, Kernel: "6.12.30-amd64", Libvirt: "10.10.0", Hostname: "lab-8cc9490gvs", Arch: "amd64", Bridge: "br0", Ready: true, CheckedAt: now.UTC().Format(time.RFC3339)}, Talos: "v1.14.2", Index: 1, VMs: vms, Metrics: &m, Updates: &u}
	if err := s.SetLabHost(ctx, mac, lh); err != nil {
		log.Fatal(err)
	}
	key := store.LabHostKey(mac)
	for t := now.Add(-24 * time.Hour); t.Before(now); t = t.Add(time.Minute) {
		age := now.Sub(t).Hours()
		cpu := 30 + 15*math.Sin(age/2) + rand.Float64()*8
		disk := m.DiskTotal - int64(float64(53*(1<<30))*(1+age/24))
		if age > 6 && age < 6.5 { // an outage hole
			continue
		}
		_ = s.AddSamples(ctx, key, t, []store.Sample{{CPUMilli: int64(cpu * 10), CPUCap: 1000, MemBytes: m.MemUsed - int64(rand.Float64()*(1<<30)), MemCap: m.MemTotal, Pods: 4, Ready: true, Reachable: true, Disk: disk, DiskCap: m.DiskTotal}})
	}
	_, _ = s.AddEvent(ctx, store.EventRow{Cluster: key, Severity: "warn", Kind: "labhost.disk-low", Message: "lab-8cc9490gvs: VM disk 88% full (412 GiB of 465 GiB)"})
	_, _ = s.AddEvent(ctx, store.EventRow{Cluster: key, Severity: "info", Kind: "labhost.updates", Message: "lab-8cc9490gvs: 12 package updates pending, reboot required"})
	log.Println("seeded", mac)
}
