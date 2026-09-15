package config_test

import (
	"testing"

	"github.com/mikael/kubit/internal/config"
)

func machines() []config.Machine {
	d := []config.MachineDisk{{DevPath: "/dev/nvme0n1", SizeBytes: 500 << 30, Transport: "nvme"}}
	return []config.Machine{
		{IP: "10.0.0.11", MAC: "aa:aa:aa:aa:aa:01", Arch: "amd64", CPUs: 4, MemBytes: 16 << 30, KVM: true, Disks: d},
		{IP: "10.0.0.12", MAC: "aa:aa:aa:aa:aa:02", Arch: "amd64", CPUs: 4, MemBytes: 8 << 30, KVM: false, Disks: d},
		{IP: "10.0.0.13", MAC: "aa:aa:aa:aa:aa:03", Arch: "amd64", CPUs: 4, MemBytes: 8 << 30, KVM: false, Disks: d},
		{IP: "10.0.0.14", MAC: "aa:aa:aa:aa:aa:04", Arch: "amd64", CPUs: 4, MemBytes: 8 << 30, KVM: false, Disks: d},
	}
}

func TestDesignPlacesControlPlanesOnSmallNonKVMMachines(t *testing.T) {
	c, warnings := config.Design("lab", machines(), config.DesignOptions{})
	if len(c.ControlPlanes()) != 3 || len(c.Workers()) != 1 {
		t.Fatalf("topology: %d cp %d workers", len(c.ControlPlanes()), len(c.Workers()))
	}
	w := c.Workers()[0]
	if w.MAC != "aa:aa:aa:aa:aa:01" || !w.KVM {
		t.Errorf("the KVM-capable, largest machine should be the worker: %+v", w)
	}
	if c.Spec.ControlPlane.VIP != "10.0.0.250" || c.Spec.Platform.MetalLB.Range != "10.0.0.200-10.0.0.220" {
		t.Errorf("network defaults: vip=%s range=%s", c.Spec.ControlPlane.VIP, c.Spec.Platform.MetalLB.Range)
	}
	if c.ControlPlanes()[0].Hostname != "lab-cp-01" || w.Hostname != "lab-worker-01" || w.InstallDisk.Path != "/dev/nvme0n1" {
		t.Errorf("naming/disk: %+v", c.Spec.Nodes)
	}
	for _, x := range warnings {
		if x.Level == "warn" {
			t.Errorf("a sound 4-machine design should not warn: %+v", x)
		}
	}
	if err := c.Validate(); err != nil {
		t.Errorf("design must validate: %v", err)
	}
}

func TestLintFindings(t *testing.T) {
	ms := machines()[:1]
	ms[0].Disks[0].SizeBytes = 8 << 30
	_, warnings := config.Design("one", ms, config.DesignOptions{MetalLBRange: "10.9.0.200-10.9.0.210"})
	codes := map[string]bool{}
	for _, w := range warnings {
		codes[w.Code] = true
	}
	for _, want := range []string{"single-control-plane", "small-disk", "metallb-off-subnet"} {
		if !codes[want] {
			t.Errorf("missing %s in %v", want, codes)
		}
	}
	two, _ := config.Design("two", machines()[:2], config.DesignOptions{})
	two.Spec.Nodes[1].Pool = "controlplane"
	two.Spec.Nodes[1].Role = config.RoleControlPlane
	found := false
	for _, w := range config.Lint(two, nil) {
		if w.Code == "even-control-planes" {
			found = true
		}
	}
	if !found {
		t.Error("two control planes must warn about the even count")
	}
}

func TestOverlaps(t *testing.T) {
	got := config.Overlaps("10.0.0.200-10.0.0.220", map[string]string{"a": "10.0.0.210-10.0.0.230", "b": "10.0.0.221-10.0.0.240", "c": "bad"})
	if len(got) != 1 || got[0] != "a" {
		t.Errorf("got %v", got)
	}
}

// Two EliteDesks, two VMs on one KVM host, one standalone box: the metal machines
// take the control plane even though the VMs are smaller.
func TestDesignPrefersBareMetalControlPlanes(t *testing.T) {
	d := []config.MachineDisk{{DevPath: "/dev/sda", SizeBytes: 256 << 30, Transport: "sata"}}
	ms := []config.Machine{
		{IP: "10.0.0.21", MAC: "aa:aa:aa:aa:aa:21", Arch: "amd64", CPUs: 8, MemBytes: 32 << 30, KVM: true, Disks: d, Model: "HP EliteDesk 800 G6"},
		{IP: "10.0.0.22", MAC: "aa:aa:aa:aa:aa:22", Arch: "amd64", CPUs: 8, MemBytes: 32 << 30, KVM: true, Disks: d, Model: "HP EliteDesk 800 G6"},
		{IP: "10.0.0.23", MAC: "aa:aa:aa:aa:aa:23", Arch: "amd64", CPUs: 2, MemBytes: 4 << 30, Virtual: true, Disks: d, Model: "QEMU Standard PC"},
		{IP: "10.0.0.24", MAC: "aa:aa:aa:aa:aa:24", Arch: "amd64", CPUs: 2, MemBytes: 4 << 30, Virtual: true, Disks: d, Model: "QEMU Standard PC"},
		{IP: "10.0.0.25", MAC: "aa:aa:aa:aa:aa:25", Arch: "amd64", CPUs: 4, MemBytes: 8 << 30, Disks: d, Model: "Intel NUC"},
	}
	c, warnings := config.Design("office", ms, config.DesignOptions{})
	cps := map[string]bool{}
	for _, n := range c.ControlPlanes() {
		cps[n.MAC] = true
	}
	for _, mac := range []string{"aa:aa:aa:aa:aa:21", "aa:aa:aa:aa:aa:22", "aa:aa:aa:aa:aa:25"} {
		if !cps[mac] {
			t.Errorf("bare-metal %s should be a control plane; control planes: %v", mac, cps)
		}
	}
	for _, w := range warnings {
		if w.Code == "control-planes-on-vms" {
			t.Errorf("no VM control planes expected, got %+v", w)
		}
	}
	// With only VMs available the lint says so.
	vms := ms[2:4]
	vms = append(vms, config.Machine{IP: "10.0.0.26", MAC: "aa:aa:aa:aa:aa:26", Arch: "amd64", CPUs: 2, MemBytes: 4 << 30, Virtual: true, Disks: d})
	_, warnings = config.Design("vms", vms, config.DesignOptions{})
	found := false
	for _, w := range warnings {
		found = found || w.Code == "control-planes-on-vms"
	}
	if !found {
		t.Errorf("expected control-planes-on-vms warning, got %+v", warnings)
	}
}
