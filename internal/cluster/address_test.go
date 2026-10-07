package cluster

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mikaelhug/kubit/internal/config"
	"github.com/mikaelhug/kubit/internal/netx"
)

const addressSpec = `
apiVersion: kubit.dev/v1
kind: Cluster
metadata: { name: lab }
spec:
  controlPlane: { endpoint: "ENDPOINT" }
  network: { nameservers: [127.0.0.53] }
  nodes:
    - { hostname: cp-01, ip: 127.0.0.1, role: controlplane, installDisk: { path: /dev/sda } }
    - { hostname: w-01, ip: 127.0.0.11, role: worker, installDisk: { path: /dev/sda }W }
`

func addressPlan(t *testing.T, endpoint, w string, live map[string]*liveNode, liveEndpoint string) *Plan {
	t.Helper()
	c, err := config.Parse([]byte(strings.NewReplacer("ENDPOINT", endpoint, "W", w).Replace(addressSpec)))
	if err != nil {
		t.Fatal(err)
	}
	ls := &liveState{exists: true, api: true, nodes: live, endpoint: liveEndpoint}
	p := &Plan{Cluster: "lab", applied: appliedSpec(c, ls)}
	planManager(t).planAddresses(context.Background(), p, &Desired{Cluster: c, Talosconfig: talosconfigFor(t)}, ls)
	return p
}

func member(addr string, a config.Addressing) *liveNode {
	return &liveNode{member: true, addr: addr, live: config.LiveConfig{Addressing: a, Found: true, Endpoint: "https://127.0.0.1:6443"}}
}

var dhcp = config.Addressing{DHCP: true}

func TestAddressChangesThatAreRefused(t *testing.T) {
	ep := "https://127.0.0.1:6443"
	live := map[string]*liveNode{"cp-01": member("127.0.0.1", dhcp), "w-01": member("127.0.0.11", dhcp)}
	p := addressPlan(t, ep, ", network: { addresses: [127.0.0.12/8], gateway: 127.0.0.2 }", live, ep)
	if len(p.Problems) != 0 {
		t.Fatalf("a free address: %v", p.Problems)
	}
	p = addressPlan(t, "https://127.0.0.99:6443", "", live, ep)
	if !strings.Contains(strings.Join(p.Problems, "\n"), "without a control plane moving") {
		t.Errorf("an endpoint change on its own: %v", p.Problems)
	}
	addressInUse = func(_ context.Context, ip string, _ time.Duration) bool { return ip == "127.0.0.12" }
	defer func() { addressInUse = netx.InUse }()
	p = addressPlan(t, ep, ", network: { addresses: [127.0.0.12/8], gateway: 127.0.0.2 }", live, ep)
	if len(p.Problems) != 1 || !strings.Contains(p.Problems[0], "127.0.0.12 already answers") {
		t.Errorf("a taken address: %v", p.Problems)
	}
}

func TestTheEndpointHolderMovesAfterTheOtherControlPlanes(t *testing.T) {
	cp := func(h, from, to string) addrChange {
		return addrChange{node: config.Node{Hostname: h, Role: config.RoleControlPlane}, from: from, to: to, kind: addrMove}
	}
	w := addrChange{node: config.Node{Hostname: "w-01", Role: config.RoleWorker}, from: "10.0.0.20", to: "10.0.0.30", kind: addrMove}
	order := addressOrder([]addrChange{w, cp("cp-01", "10.0.0.10", "10.0.0.51"), cp("cp-02", "10.0.0.11", "10.0.0.52")}, "10.0.0.51")
	var got []string
	for _, ch := range order {
		got = append(got, ch.node.Hostname)
	}
	if strings.Join(got, ",") != "cp-02,cp-01,w-01" {
		t.Errorf("order %v", got)
	}
}
