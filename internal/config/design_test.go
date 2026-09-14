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
