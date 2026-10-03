package cluster

import (
	"bytes"
	"context"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/k8s"
	"k8s.io/client-go/kubernetes/fake"
)

func TestBridgeSubnetsAllowsTheOldAndNewSubnet(t *testing.T) {
	spec := `
apiVersion: kubit.dev/v1
kind: Cluster
metadata: { name: lab }
spec:
  network: { firewall: true }
  nodes:
    - { hostname: cp-01, ip: 10.0.0.1, role: controlplane, installDisk: { path: /dev/sda } }
    - { hostname: cp-02, ip: 10.0.0.2, role: controlplane, installDisk: { path: /dev/sda } }
    - { hostname: w-01, ip: 10.0.2.3, role: worker, installDisk: { path: /dev/sda } }
`
	c, err := config.Parse([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	old := c.Spec.Nodes[2]
	c.Spec.Nodes[2].IP = "10.0.1.3"
	bridge := bridgeSubnets(c, 2, old)
	pfx := netip.MustParsePrefix
	if got, want := config.ClusterSubnets(bridge), []netip.Prefix{pfx("10.0.0.0/24"), pfx("10.0.1.0/24"), pfx("10.0.2.0/24")}; !slices.Equal(got, want) {
		t.Errorf("bridge subnets: %v, want %v", got, want)
	}
	if got, want := config.ClusterSubnets(c), []netip.Prefix{pfx("10.0.0.0/24"), pfx("10.0.1.0/24")}; !slices.Equal(got, want) || c.Spec.Nodes[2].Network != nil {
		t.Errorf("the declaration must stay untouched: %v %+v", got, c.Spec.Nodes[2].Network)
	}
	bundle, err := trialBundle(c.Spec.TalosVersion)
	if err != nil {
		t.Fatal(err)
	}
	installer := config.FixedInstaller("factory.talos.dev/installer/x:v1.14.2")
	bridged, err := config.Generate(bridge, bundle, installer)
	if err != nil {
		t.Fatal(err)
	}
	moved, err := config.Generate(c, bundle, installer)
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"cp-01", "cp-02"} {
		cfg := bridged.Nodes[host]
		if !bytes.Contains(cfg, []byte("10.0.1.0/24")) || !bytes.Contains(cfg, []byte("10.0.2.0/24")) {
			t.Errorf("%s must allow the old and the new subnet:\n%s", host, cfg)
		}
		if bytes.Contains(moved.Nodes[host], []byte("10.0.2.0/24")) {
			t.Errorf("%s: the narrowed config must drop the old subnet", host)
		}
	}
}

func TestApplyOthersSkipsUpToDateNodes(t *testing.T) {
	st := testStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m := NewManager(st, t.TempDir())
	c, err := config.Parse([]byte(threeControlPlanes))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SaveCluster(ctx, c, StateReady); err != nil {
		t.Fatal(err)
	}
	cfgs := map[string][]byte{}
	for _, n := range c.Spec.Nodes {
		cfgs[n.Hostname] = []byte("cfg " + n.Hostname)
		if n.Hostname == "cp-03" {
			continue
		}
		if err := m.recordNode(ctx, c, n, NodeReady, cfgs[n.Hostname]); err != nil {
			t.Fatal(err)
		}
		if err := st.PutNodeMachineConfig(ctx, n.IP, cfgs[n.Hostname], false); err != nil {
			t.Fatal(err)
		}
	}
	kc := &k8s.Client{Interface: fake.NewClientset()}
	if err := m.applyOthers(ctx, c, kc, "cp-03", cfgs, nil, "firewall", func(Event) { t.Error("no event for an up-to-date node") }); err != nil {
		t.Errorf("only the skipped node is behind: %v", err)
	}
}
