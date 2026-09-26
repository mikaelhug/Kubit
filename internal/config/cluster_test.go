package config_test

import (
	"strings"
	"testing"

	"github.com/mikael/kubit/internal/config"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	"github.com/siderolabs/talos/pkg/machinery/gendata"
)

func TestParseDefaults(t *testing.T) {
	c, err := config.Parse([]byte(sampleCluster))
	if err != nil {
		t.Fatal(err)
	}
	if c.Spec.TalosVersion != gendata.VersionTag {
		t.Errorf("talosVersion = %q, want machinery's %q", c.Spec.TalosVersion, gendata.VersionTag)
	}
	if c.Spec.KubernetesVersion != "v"+constants.DefaultKubernetesVersion {
		t.Errorf("kubernetesVersion = %q", c.Spec.KubernetesVersion)
	}
	if c.Spec.ControlPlane.Endpoint != "https://192.168.64.9:6443" {
		t.Errorf("endpoint should default to the VIP, got %q", c.Spec.ControlPlane.Endpoint)
	}
	if c.Spec.ControlPlane.AllowScheduling == nil || !*c.Spec.ControlPlane.AllowScheduling {
		t.Error("4 nodes: control planes should be schedulable by default")
	}
	if c.Spec.Network.PodCIDR != "10.244.0.0/16" || c.Spec.Network.ServiceCIDR != "10.96.0.0/12" {
		t.Errorf("CIDR defaults: %+v", c.Spec.Network)
	}
	if len(c.Spec.Extensions) != 1 || c.Spec.Extensions[0] != "siderolabs/gvisor" {
		t.Errorf("gvisor enabled should imply the extension, got %v", c.Spec.Extensions)
	}
	if len(c.ControlPlanes()) != 3 || len(c.Workers()) != 1 {
		t.Errorf("roles: %d cp, %d workers", len(c.ControlPlanes()), len(c.Workers()))
	}
}

func TestPrereleaseTalosVersionAccepted(t *testing.T) {
	c, err := config.Parse([]byte(strings.Replace(sampleCluster, "spec:\n", "spec:\n  talosVersion: v1.14.0-rc.2\n", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if c.Spec.TalosVersion != "v1.14.0-rc.2" {
		t.Error(c.Spec.TalosVersion)
	}
}

func TestEndpointDefaultsToFirstControlPlaneWithoutVIP(t *testing.T) {
	c, err := config.Parse([]byte(strings.Replace(sampleCluster, "vip: 192.168.64.9", "", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if c.Spec.ControlPlane.Endpoint != "https://192.168.64.2:6443" {
		t.Errorf("endpoint = %q", c.Spec.ControlPlane.Endpoint)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]string{
		"unknown field":      strings.Replace(sampleCluster, "kind: Cluster", "kind: Cluster\nbogus: 1", 1),
		"duplicate ip":       strings.Replace(sampleCluster, "192.168.64.5", "192.168.64.2", 1),
		"duplicate host":     strings.Replace(sampleCluster, "worker-01", "cp-01", 1),
		"bad role":           strings.Replace(sampleCluster, "role: worker", "role: minion", 1),
		"no control plane":   strings.ReplaceAll(sampleCluster, "role: controlplane", "role: worker"),
		"bad mac":            strings.Replace(sampleCluster, "52:54:00:4b:49:01", "nope", 1),
		"disk path+selector": strings.Replace(sampleCluster, "installDisk: { path: /dev/vda }", "installDisk: { path: /dev/vda, selector: { type: nvme } }", 1),
		"no disk":            strings.Replace(sampleCluster, "installDisk: { path: /dev/vda }", "installDisk: {}", 1),
		"bad metallb range":  strings.Replace(sampleCluster, "192.168.64.200-192.168.64.220", "192.168.64.220-192.168.64.200", 1),
		"uppercase hostname": strings.Replace(sampleCluster, "hostname: cp-01", "hostname: CP-01", 1),
		"talos too old":      strings.Replace(sampleCluster, "spec:\n", "spec:\n  talosVersion: v1.13.10\n", 1),
		"static without dns": strings.Replace(sampleCluster, `mac: "52:54:00:4b:49:01", installDisk: { path: /dev/vda }`, `mac: "52:54:00:4b:49:01", installDisk: { path: /dev/vda }, network: { addresses: ["192.168.64.2/24"] }`, 1),
		"flux over http":     sampleCluster + "    flux: { enabled: true, repository: { url: http://example.com/apps.git } }\n",
		"flux path escapes":  sampleCluster + "    flux: { enabled: true, repository: { url: https://example.com/apps.git, path: ../other } }\n",
		"flux interval":      sampleCluster + "    flux: { enabled: true, repository: { url: https://example.com/apps.git, interval: soon } }\n",
	}
	for name, doc := range cases {
		if _, err := config.Parse([]byte(doc)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	c, err := config.Parse([]byte(sampleCluster))
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	c2, err := config.Parse(b)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if b2, _ := c2.Marshal(); string(b2) != string(b) {
		t.Errorf("marshal not stable:\n%s\n---\n%s", b, b2)
	}
}

func TestFluxRepositoryDefaults(t *testing.T) {
	c, err := config.Parse([]byte(sampleCluster + "    flux: { enabled: true, repository: { url: https://github.com/mikaelhug/kubit-apps.git } }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if r := c.Spec.Platform.Flux.Repository; r.Branch != "main" || r.Path != "./" || r.Interval != "5m" {
		t.Errorf("repository = %+v", r)
	}
}

func TestParseIPRange(t *testing.T) {
	a, b, err := config.ParseIPRange("10.0.0.5 - 10.0.0.9")
	if err != nil || a.String() != "10.0.0.5" || b.String() != "10.0.0.9" {
		t.Errorf("got %v %v %v", a, b, err)
	}
	if _, _, err := config.ParseIPRange("10.0.0.5"); err == nil {
		t.Error("single address should be rejected")
	}
}

func TestRecommend(t *testing.T) {
	cases := []struct {
		n     int
		cp, w int
		sched bool
	}{
		{1, 1, 0, true}, {2, 1, 1, true}, {3, 3, 0, true}, {5, 3, 2, true}, {6, 3, 3, false}, {10, 3, 7, false},
	}
	for _, tc := range cases {
		got := config.Recommend(tc.n)
		if got.ControlPlanes != tc.cp || got.Workers != tc.w || got.AllowScheduling != tc.sched {
			t.Errorf("Recommend(%d) = %+v, want cp=%d w=%d sched=%v", tc.n, got, tc.cp, tc.w, tc.sched)
		}
		if got.HA != (tc.cp == 3) {
			t.Errorf("Recommend(%d).HA = %v", tc.n, got.HA)
		}
	}
}
