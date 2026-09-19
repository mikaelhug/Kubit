package watch

import (
	"testing"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/store"
)

func status(api bool, etcd bool, nodes ...cluster.NodeStatus) *cluster.Status {
	return &cluster.Status{APIReachable: api, Etcd: cluster.EtcdStatus{Expected: 1, Members: 1, Healthy: etcd}, Nodes: nodes}
}

func ckinds(evs []store.EventRow) []string {
	var out []string
	for _, e := range evs {
		out = append(out, e.Kind)
	}
	return out
}

func TestConfirmRaisesOnThirdTickAndClearsAtOnce(t *testing.T) {
	c := newConfirm()
	good := status(true, true, cluster.NodeStatus{Hostname: "a", TalosReachable: true, Registered: true, Ready: true})
	bad := status(false, false, cluster.NodeStatus{Hostname: "a", TalosReachable: false, TalosError: "port closed", Registered: true, Ready: true})
	if evs := c.Apply("c", good); len(evs) != 0 {
		t.Fatalf("healthy: %v", ckinds(evs))
	}
	for i := 1; i < confirmAfter; i++ {
		if evs := c.Apply("c", bad); len(evs) != 0 {
			t.Fatalf("tick %d must not alert yet: %v", i, ckinds(evs))
		}
	}
	evs := c.Apply("c", bad)
	if len(evs) != 3 {
		t.Fatalf("third tick must raise three alerts, got %v", ckinds(evs))
	}
	if evs := c.Apply("c", bad); len(evs) != 0 {
		t.Fatalf("open alerts are not re-raised: %v", ckinds(evs))
	}
	evs = c.Apply("c", good)
	if len(evs) != 3 {
		t.Fatalf("recovery must close all three at once, got %v", ckinds(evs))
	}
	for _, e := range evs {
		if e.Severity != "info" {
			t.Errorf("recovery %s must be info", e.Kind)
		}
	}
}

func TestConfirmBlipIsSilent(t *testing.T) {
	c := newConfirm()
	good := status(true, true)
	bad := status(false, true)
	c.Apply("c", good)
	if evs := c.Apply("c", bad); len(evs) != 0 {
		t.Fatalf("one failed tick: %v", ckinds(evs))
	}
	if evs := c.Apply("c", good); len(evs) != 0 {
		t.Fatalf("recovery of an unraised alert must be silent: %v", ckinds(evs))
	}
}

func TestConfirmResetAfterGap(t *testing.T) {
	c := newConfirm()
	bad := status(false, true)
	c.Apply("c", bad)
	c.Apply("c", bad)
	c.Reset()
	if evs := c.Apply("c", bad); len(evs) != 0 {
		t.Fatalf("counting restarts after a gap: %v", ckinds(evs))
	}
}

func TestConfirmSeedKeepsOpenAlerts(t *testing.T) {
	c := newConfirm()
	c.Seed([]store.EventRow{{Kind: "api.unreachable", Severity: "critical"}})
	if evs := c.Apply("c", status(false, true)); len(evs) != 0 {
		t.Fatalf("seeded alert must not be re-raised: %v", ckinds(evs))
	}
	evs := c.Apply("c", status(true, true))
	if len(evs) != 1 || evs[0].Kind != "api.back" {
		t.Fatalf("seeded alert must clear on recovery, got %v", ckinds(evs))
	}
}

func TestUnconfirmedDropsReachabilityKinds(t *testing.T) {
	in := []store.EventRow{{Kind: "talos.unreachable"}, {Kind: "talos.back"}, {Kind: "talos.version"}, {Kind: "api.unreachable"}, {Kind: "etcd.members"}}
	got := ckinds(unconfirmed(in))
	if len(got) != 2 || got[0] != "talos.version" || got[1] != "etcd.members" {
		t.Fatalf("got %v", got)
	}
}
