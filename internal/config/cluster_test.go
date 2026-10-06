package config_test

import (
	"strings"
	"testing"

	"github.com/mikael/kubit/internal/config"
)

func TestConflictingOrUnbootableClusterIsRefused(t *testing.T) {
	cases := map[string]string{
		"duplicate ip":     strings.Replace(sampleCluster, "192.168.64.5", "192.168.64.2", 1),
		"duplicate host":   strings.Replace(sampleCluster, "worker-01", "cp-01", 1),
		"no control plane": strings.ReplaceAll(sampleCluster, "role: controlplane", "role: worker"),
		"talos too old":    strings.Replace(sampleCluster, "spec:\n", "spec:\n  talosVersion: v1.13.10\n", 1),
	}
	for name, doc := range cases {
		if _, err := config.Parse([]byte(doc)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

const declaredExtras = `  network: { policies: false, discovery: false }
  storage: { systemDisk: true, ephemeralSize: 60GiB }
  patches:
    - machine: { sysctls: { vm.max_map_count: "262144" } }
`

func TestMarshalKeepsEveryDeclaredField(t *testing.T) {
	c, err := config.Parse([]byte(strings.Replace(strings.Replace(sampleCluster, "spec:\n", "spec:\n"+declaredExtras, 1),
		"kvm: true,", "kvm: true, labels: { tier: edge }, taints: { dedicated: \"edge:NoSchedule\" }, annotations: { owner: ops }, patches: [ { machine: { install: { wipe: false } } } ],", 1)))
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
	nw, w := c2.Spec.Network, c2.Spec.Nodes[3]
	if *nw.Policies || *nw.Discovery || !c2.Spec.Storage.SystemDisk || c2.Spec.Storage.EphemeralSize != "60GiB" {
		t.Errorf("network %+v storage %+v", nw, c2.Spec.Storage)
	}
	if len(c2.Spec.Patches) != 1 || len(w.Patches) != 1 || w.Labels["tier"] != "edge" || w.Taints["dedicated"] != "edge:NoSchedule" || w.Annotations["owner"] != "ops" {
		t.Errorf("patches %v / %v, node %+v", c2.Spec.Patches, w.Patches, w)
	}
	if b2, _ := c2.Marshal(); string(b2) != string(b) {
		t.Errorf("marshal not stable:\n%s\n---\n%s", b, b2)
	}
}

func TestEndpointHostIsFoundForTheRemovalGuard(t *testing.T) {
	c, err := config.Parse([]byte(strings.Replace(sampleCluster, "    vip: 192.168.64.9\n", "", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if n, ok := c.EndpointNode(); !ok || n.Hostname != "cp-01" {
		t.Errorf("without a VIP the first control plane hosts the endpoint: %v %v", n.Hostname, ok)
	}
	vip, err := config.Parse([]byte(sampleCluster))
	if err != nil {
		t.Fatal(err)
	}
	if n, ok := vip.EndpointNode(); ok {
		t.Errorf("a VIP endpoint has no host node, got %s", n.Hostname)
	}
}
