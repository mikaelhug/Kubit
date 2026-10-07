package watch

import (
	"testing"
	"time"

	"github.com/mikaelhug/kubit/internal/cluster"
)

func status(api bool, etcd bool, nodes ...cluster.NodeStatus) *cluster.Status {
	return &cluster.Status{APIReachable: api, Etcd: cluster.EtcdStatus{Expected: 1, Members: 1, Healthy: etcd}, Nodes: nodes}
}

func apply(c *confirm, st *cluster.Status) []transition { return c.Apply("c", badFacts("c", st)) }

func tkinds(ts []transition) []string {
	var out []string
	for _, t := range ts {
		if t.raise != nil {
			out = append(out, t.raise.Kind)
		} else {
			out = append(out, t.recovery.Kind)
		}
	}
	return out
}

func TestConfirmRaisesOnThirdTickAndClearsAtOnce(t *testing.T) {
	c := newConfirm()
	good := status(true, true, cluster.NodeStatus{Hostname: "a", TalosReachable: true, Registered: true, Ready: true})
	bad := status(false, false, cluster.NodeStatus{Hostname: "a", TalosReachable: false, TalosError: "port closed", Registered: true, Ready: true})
	if ts := apply(c, good); len(ts) != 0 {
		t.Fatalf("healthy: %v", tkinds(ts))
	}
	for i := 1; i < confirmAfter; i++ {
		if ts := apply(c, bad); len(ts) != 0 {
			t.Fatalf("tick %d must not alert yet: %v", i, tkinds(ts))
		}
	}
	ts := apply(c, bad)
	if len(ts) != 3 {
		t.Fatalf("third tick must raise three alerts, got %v", tkinds(ts))
	}
	for _, tr := range ts {
		if tr.raise == nil || !tr.raise.Notify {
			t.Errorf("a transition seen by this process notifies: %+v", tr)
		}
	}
	if ts := apply(c, bad); len(ts) != 0 {
		t.Fatalf("open alerts are not re-raised: %v", tkinds(ts))
	}
	ts = apply(c, good)
	if len(ts) != 3 {
		t.Fatalf("recovery must close all three at once, got %v", tkinds(ts))
	}
	for _, tr := range ts {
		if tr.raise != nil || tr.recovery.Severity != "info" || tr.resolves == "" {
			t.Errorf("recovery %+v", tr)
		}
	}
}

func TestConfirmBlipIsSilent(t *testing.T) {
	c := newConfirm()
	apply(c, status(true, true))
	if ts := apply(c, status(false, true)); len(ts) != 0 {
		t.Fatalf("one failed tick: %v", tkinds(ts))
	}
	if ts := apply(c, status(true, true)); len(ts) != 0 {
		t.Fatalf("recovery of an unraised alert must be silent: %v", tkinds(ts))
	}
}

func TestConfirmRestartsCountingAfterASleep(t *testing.T) {
	c := newConfirm()
	bad := status(false, true)
	apply(c, bad)
	apply(c, bad)
	c.Reset()
	if ts := apply(c, bad); len(ts) != 0 {
		t.Fatalf("counting restarts after a gap: %v", tkinds(ts))
	}
}

func TestRestartedLoopDoesNotReRaiseOpenAlerts(t *testing.T) {
	c := newConfirm()
	c.Seed([]Event{{Kind: "api.unreachable", Severity: "critical", Open: true}})
	if ts := apply(c, status(false, true)); len(ts) != 0 {
		t.Fatalf("seeded alert must not be re-raised: %v", tkinds(ts))
	}
	ts := apply(c, status(true, true))
	if len(ts) != 1 || ts[0].recovery.Kind != "api.back" || ts[0].resolves != "api.unreachable" {
		t.Fatalf("seeded alert must clear on recovery, got %v", tkinds(ts))
	}
}

func TestATickSpanningASleepIsAGap(t *testing.T) {
	iv := 15 * time.Second
	t0 := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
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

func TestObserverGoesOfflineOnlyAfterThreeBlindTicks(t *testing.T) {
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

func TestOneSuspensionIsOneGap(t *testing.T) {
	w := &Watcher{observer: ObserverState{Online: true}}
	w.interval = 15 * time.Second
	var seen []ObserverState
	w.OnObserver = func(o ObserverState) { seen = append(seen, o) }
	wake := time.Now()
	for i := range 4 {
		w.noteGap(wake.Add(time.Duration(i) * time.Second))
	}
	if o := w.Observer(); o.Gaps24h != 1 {
		t.Errorf("one sleep seen by 4 cluster loops counted %d gaps", o.Gaps24h)
	}
	if len(seen) != 1 || seen[0].Gaps24h != 1 || seen[0].LastGapAt == "" || !seen[0].Online {
		t.Fatalf("a gap must publish the observer state: %+v", seen)
	}
	w.noteGap(wake.Add(10 * time.Minute))
	if len(seen) != 2 || seen[1].Gaps24h != 2 {
		t.Errorf("a later sleep is a second gap: %+v", seen)
	}
}
