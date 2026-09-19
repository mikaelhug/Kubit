package watch

import (
	"testing"
	"time"

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

func TestIsGap(t *testing.T) {
	iv := 15 * time.Second
	t0 := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	if isGap(time.Time{}, t0, t0.Add(time.Second), iv) {
		t.Error("first tick is never a gap")
	}
	if isGap(t0, t0.Add(15*time.Second), t0.Add(16*time.Second), iv) {
		t.Error("a regular tick is not a gap")
	}
	if !isGap(t0, t0.Add(20*time.Minute), t0.Add(20*time.Minute+time.Second), iv) {
		t.Error("a tick 20 min after the previous one is a gap")
	}
	if !isGap(t0, t0.Add(15*time.Second), t0.Add(10*time.Minute), iv) {
		t.Error("a tick that spanned a suspension is a gap")
	}
}

func TestOfflineNeedsConfirmation(t *testing.T) {
	w := &Watcher{observer: ObserverState{Online: true}}
	var flips []bool
	w.OnObserver = func(o ObserverState) { flips = append(flips, o.Online) }
	w.noteOffline("no route to host")
	w.noteOffline("no route to host")
	if len(flips) != 0 {
		t.Fatalf("two blind ticks must not flip: %v", flips)
	}
	w.resetOffline()
	w.noteOffline("no route to host")
	w.noteOffline("no route to host")
	if len(flips) != 0 {
		t.Fatalf("a gap restarts the count: %v", flips)
	}
	w.noteOffline("no route to host")
	if len(flips) != 1 || flips[0] {
		t.Fatalf("third blind tick flips offline: %v", flips)
	}
	w.noteOnline()
	if len(flips) != 2 || !flips[1] {
		t.Fatalf("one good tick flips back online: %v", flips)
	}
}
