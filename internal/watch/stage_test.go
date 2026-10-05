package watch

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/k8s"
	"github.com/mikael/kubit/internal/store"
)

type stageFeed struct {
	ctx         context.Context
	ip          string
	talosconfig string
	report      func(string)
}

type stagePublished struct {
	mu        sync.Mutex
	statuses  []*cluster.Status
	refreshes []string
	events    []store.EventRow
}

func (p *stagePublished) counts() (int, int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.statuses), len(p.refreshes), len(p.events)
}

func stageWatcher(t *testing.T) (*Watcher, chan stageFeed, *stagePublished) {
	t.Helper()
	w := testWatcher(t)
	ctx := t.Context()
	if err := w.Store.PutCluster(ctx, store.ClusterRow{Name: "c", Spec: []byte("x"), State: "ready"}); err != nil {
		t.Fatal(err)
	}
	if err := w.Store.PutClusterSecrets(ctx, "c", store.ClusterSecrets{SecretsBundle: []byte("b"), Talosconfig: []byte("t1")}); err != nil {
		t.Fatal(err)
	}
	feeds := make(chan stageFeed, 16)
	w.watchStage = func(ctx context.Context, ip string, talosconfig []byte, report func(string)) error {
		feeds <- stageFeed{ctx: ctx, ip: ip, talosconfig: string(talosconfig), report: report}
		<-ctx.Done()
		return ctx.Err()
	}
	pub := &stagePublished{}
	w.OnStatus = func(_ string, st *cluster.Status) {
		pub.mu.Lock()
		pub.statuses = append(pub.statuses, st)
		pub.mu.Unlock()
	}
	w.OnRefresh = func(_, scope string) {
		pub.mu.Lock()
		pub.refreshes = append(pub.refreshes, scope)
		pub.mu.Unlock()
	}
	w.OnEvent = func(e store.EventRow) {
		pub.mu.Lock()
		pub.events = append(pub.events, e)
		pub.mu.Unlock()
	}
	return w, feeds, pub
}

func stageNode(host, ip, stage string, reachable bool) cluster.NodeStatus {
	return cluster.NodeStatus{Hostname: host, IP: ip, Stage: stage, TalosReachable: reachable, TalosVersion: "v1.14.0"}
}

func stageStatus(nodes ...cluster.NodeStatus) *cluster.Status {
	return &cluster.Status{Name: "c", State: cluster.StateReady, Nodes: nodes}
}

func (w *Watcher) setLatest(name string, st *cluster.Status) {
	w.mu.Lock()
	w.last[name] = st
	w.mu.Unlock()
}

func nextFeeds(t *testing.T, feeds chan stageFeed, n int) map[string]stageFeed {
	t.Helper()
	out := map[string]stageFeed{}
	for range n {
		select {
		case f := <-feeds:
			out[f.ip] = f
		case <-time.After(5 * time.Second):
			t.Fatalf("expected %d stage watches, got %d", n, len(out))
		}
	}
	return out
}

func noFeed(t *testing.T, feeds chan stageFeed) {
	t.Helper()
	select {
	case f := <-feeds:
		t.Fatalf("unexpected stage watch for %s", f.ip)
	case <-time.After(50 * time.Millisecond):
	}
}

func cancelled(t *testing.T, f stageFeed) {
	t.Helper()
	select {
	case <-f.ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatalf("stage watch for %s still running", f.ip)
	}
}

func running(t *testing.T, f stageFeed) {
	t.Helper()
	if f.ctx.Err() != nil {
		t.Fatalf("stage watch for %s was cancelled", f.ip)
	}
}

func TestStagePushPatchesLatestWithoutTouchingConfirm(t *testing.T) {
	w, feeds, pub := stageWatcher(t)
	base := stageStatus(stageNode("a", "10.0.0.1", "running", true), stageNode("b", "10.0.0.2", "running", true))
	tickAt := time.Now().Add(-time.Second)
	c := newConfirm()
	w.mu.Lock()
	w.last["c"] = base
	w.confirms["c"] = c
	w.lastTick["c"] = tickAt
	w.mu.Unlock()
	w.syncStageWatches(t.Context(), "c", base, false)
	got := nextFeeds(t, feeds, 2)
	if got["10.0.0.1"].talosconfig != "t1" {
		t.Errorf("watch dialled with talosconfig %q", got["10.0.0.1"].talosconfig)
	}
	got["10.0.0.1"].report("running")
	if s, r, _ := pub.counts(); s != 0 || r != 0 {
		t.Fatalf("an unchanged stage published %d statuses and %d refreshes", s, r)
	}
	got["10.0.0.1"].report("rebooting")
	latest := w.Latest("c")
	if latest.Nodes[0].Stage != "rebooting" || latest.Nodes[1].Stage != "running" {
		t.Fatalf("latest stages %q %q", latest.Nodes[0].Stage, latest.Nodes[1].Stage)
	}
	pub.mu.Lock()
	statuses, refreshes := pub.statuses, pub.refreshes
	pub.mu.Unlock()
	if len(statuses) != 1 || statuses[0] != latest {
		t.Errorf("published %d statuses, want the patched latest once", len(statuses))
	}
	if len(refreshes) != 1 || refreshes[0] != k8s.ScopeNodes {
		t.Errorf("refreshes %v, want one nodes refresh", refreshes)
	}
	w.mu.Lock()
	sameConfirm, sameTick := w.confirms["c"] == c, w.lastTick["c"].Equal(tickAt)
	w.mu.Unlock()
	if !sameConfirm || !sameTick || len(c.bad) != 0 || len(c.open) != 0 {
		t.Error("a stage push touched confirmation or the tick clock")
	}
	if _, _, e := pub.counts(); e != 0 || w.Store.OpenEventCount(t.Context(), "c") != 0 {
		t.Error("a stage push emitted events")
	}
	next := stageStatus(stageNode("a", "10.0.0.1", "rebooting", true), stageNode("b", "10.0.0.2", "running", true))
	if talosChanged(latest, next) {
		t.Error("the next tick must see no stage difference")
	}
}

func TestStagePushIsCopyOnWrite(t *testing.T) {
	w, feeds, _ := stageWatcher(t)
	base := stageStatus(stageNode("a", "10.0.0.1", "running", true), stageNode("b", "10.0.0.2", "running", true))
	w.setLatest("c", base)
	w.syncStageWatches(t.Context(), "c", base, false)
	got := nextFeeds(t, feeds, 2)
	stop := make(chan struct{})
	var reads atomic.Int64
	var readers sync.WaitGroup
	readers.Add(1)
	go func() {
		defer readers.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			for _, n := range base.Nodes {
				if n.Stage != "running" {
					t.Error("the shared status changed under a reader")
				}
			}
			reads.Add(1)
		}
	}()
	for reads.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	got["10.0.0.1"].report("rebooting")
	got["10.0.0.2"].report("booting")
	close(stop)
	readers.Wait()
	latest := w.Latest("c")
	if latest == base || &latest.Nodes[0] == &base.Nodes[0] {
		t.Fatal("the push reused the shared status")
	}
	if base.Nodes[0].Stage != "running" || base.Nodes[1].Stage != "running" {
		t.Errorf("the previous status was mutated: %q %q", base.Nodes[0].Stage, base.Nodes[1].Stage)
	}
	if latest.Nodes[0].Stage != "rebooting" || latest.Nodes[1].Stage != "booting" {
		t.Errorf("latest stages %q %q", latest.Nodes[0].Stage, latest.Nodes[1].Stage)
	}
}

func TestStageWatchRestartsOnGap(t *testing.T) {
	w, feeds, pub := stageWatcher(t)
	base := stageStatus(stageNode("a", "10.0.0.1", "running", true), stageNode("b", "10.0.0.2", "running", true))
	w.setLatest("c", base)
	w.syncStageWatches(t.Context(), "c", base, false)
	before := nextFeeds(t, feeds, 2)
	w.syncStageWatches(t.Context(), "c", base, true)
	for _, f := range before {
		cancelled(t, f)
	}
	noFeed(t, feeds)
	before["10.0.0.1"].report("rebooting")
	if s, _, _ := pub.counts(); s != 0 || w.Latest("c").Nodes[0].Stage != "running" {
		t.Error("a watch cancelled by the gap still patched the status")
	}
	w.syncStageWatches(t.Context(), "c", base, false)
	after := nextFeeds(t, feeds, 2)
	for _, f := range after {
		running(t, f)
	}
	after["10.0.0.1"].report("rebooting")
	if w.Latest("c").Nodes[0].Stage != "rebooting" {
		t.Error("the watch started after the gap did not push")
	}
}

func TestStageWatchStopsForRemovedNode(t *testing.T) {
	w, feeds, _ := stageWatcher(t)
	base := stageStatus(stageNode("a", "10.0.0.1", "running", true), stageNode("b", "10.0.0.2", "running", true), stageNode("c", "10.0.0.3", "running", true))
	w.setLatest("c", base)
	w.syncStageWatches(t.Context(), "c", base, false)
	first := nextFeeds(t, feeds, 3)
	cur := stageStatus(stageNode("a", "10.0.0.1", "running", true), stageNode("c", "10.0.0.3", "running", false))
	w.setLatest("c", cur)
	w.syncStageWatches(t.Context(), "c", cur, false)
	cancelled(t, first["10.0.0.2"])
	cancelled(t, first["10.0.0.3"])
	running(t, first["10.0.0.1"])
	noFeed(t, feeds)
	moved := stageStatus(stageNode("a", "10.0.0.9", "running", true))
	w.setLatest("c", moved)
	w.syncStageWatches(t.Context(), "c", moved, false)
	cancelled(t, first["10.0.0.1"])
	if f := nextFeeds(t, feeds, 1); f["10.0.0.9"].report == nil {
		t.Fatal("no watch on the new address")
	}
	sec, _ := w.Store.GetClusterSecrets(t.Context(), "c")
	sec.Talosconfig = []byte("t2")
	if err := w.Store.PutClusterSecrets(t.Context(), "c", *sec); err != nil {
		t.Fatal(err)
	}
	w.syncStageWatches(t.Context(), "c", moved, false)
	rotated := nextFeeds(t, feeds, 1)["10.0.0.9"]
	if rotated.talosconfig != "t2" {
		t.Errorf("restarted with talosconfig %q", rotated.talosconfig)
	}
	w.forget("c", nil)
	cancelled(t, rotated)
	w.mu.Lock()
	left := len(w.stages)
	w.mu.Unlock()
	if left != 0 {
		t.Errorf("forget left %d clusters with stage watches", left)
	}
}

func TestStaleStageEventDropped(t *testing.T) {
	w, feeds, pub := stageWatcher(t)
	base := stageStatus(stageNode("a", "10.0.0.1", "running", true), stageNode("b", "10.0.0.2", "running", true))
	w.setLatest("c", base)
	w.syncStageWatches(t.Context(), "c", base, false)
	got := nextFeeds(t, feeds, 2)
	down := stageStatus(stageNode("a", "10.0.0.1", "", false), stageNode("b", "10.0.0.2", "running", true))
	w.setLatest("c", down)
	got["10.0.0.1"].report("rebooting")
	if w.Latest("c") != down {
		t.Error("an event for a node the newer tick found unreachable patched the status")
	}
	readdressed := stageStatus(stageNode("a", "10.0.0.1", "running", true), stageNode("b", "10.0.0.7", "running", true))
	w.setLatest("c", readdressed)
	got["10.0.0.2"].report("rebooting")
	if w.Latest("c") != readdressed {
		t.Error("an event for an address no longer in the status patched it")
	}
	w.syncStageWatches(t.Context(), "c", readdressed, false)
	cancelled(t, got["10.0.0.2"])
	nextFeeds(t, feeds, 1)
	got["10.0.0.2"].report("booting")
	if w.Latest("c") != readdressed {
		t.Error("an event from a replaced watch patched the status")
	}
	w.forget("c", nil)
	got["10.0.0.1"].report("rebooting")
	if w.Latest("c") != nil {
		t.Error("an event after forget recreated the status")
	}
	if s, r, _ := pub.counts(); s != 0 || r != 0 {
		t.Errorf("stale events published %d statuses and %d refreshes", s, r)
	}
}

func TestEndedStageWatchRestartsOnNextTick(t *testing.T) {
	w, _, _ := stageWatcher(t)
	var starts atomic.Int64
	ended := make(chan struct{}, 4)
	w.watchStage = func(context.Context, string, []byte, func(string)) error {
		starts.Add(1)
		ended <- struct{}{}
		return errors.New("stream reset")
	}
	base := stageStatus(stageNode("a", "10.0.0.1", "running", true))
	w.setLatest("c", base)
	w.syncStageWatches(t.Context(), "c", base, false)
	<-ended
	time.Sleep(50 * time.Millisecond)
	if n := starts.Load(); n != 1 {
		t.Fatalf("an ended watch restarted on its own: %d starts", n)
	}
	w.syncStageWatches(t.Context(), "c", base, false)
	<-ended
	if n := starts.Load(); n != 2 {
		t.Errorf("the next tick did not restart the ended watch: %d starts", n)
	}
}

func TestStageWatchRestartsWhenTickDisagrees(t *testing.T) {
	w, feeds, _ := stageWatcher(t)
	base := stageStatus(stageNode("a", "10.0.0.1", "running", true))
	w.setLatest("c", base)
	w.syncStageWatches(t.Context(), "c", base, false)
	first := nextFeeds(t, feeds, 1)["10.0.0.1"]
	first.report("running")
	w.syncStageWatches(t.Context(), "c", base, false)
	running(t, first)
	noFeed(t, feeds)
	missed := stageStatus(stageNode("a", "10.0.0.1", "booting", true))
	w.setLatest("c", missed)
	w.syncStageWatches(t.Context(), "c", missed, false)
	cancelled(t, first)
	nextFeeds(t, feeds, 1)
}
