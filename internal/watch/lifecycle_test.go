package watch

import (
	"testing"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/k8s"
	"github.com/mikael/kubit/internal/store"
)

func TestSetIntervalRetunesRunningTickers(t *testing.T) {
	w, _ := labWatcher(t)
	ch := w.retuned()
	w.SetInterval(time.Minute)
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("a changed interval must wake the running tickers")
	}
	if w.Interval() != time.Minute {
		t.Errorf("interval %s", w.Interval())
	}
	ch = w.retuned()
	w.SetInterval(time.Minute)
	select {
	case <-ch:
		t.Error("an unchanged interval must not retune")
	default:
	}
}

func TestStoppedLoopForgetsItsClusterBeforeARestart(t *testing.T) {
	w, _ := labWatcher(t)
	w.SetInterval(time.Hour)
	ctx := t.Context()
	w.mu.Lock()
	w.startLoop(ctx, "gone")
	first := w.running["gone"]
	w.confirms["gone"] = newConfirm()
	w.lastTick["gone"] = time.Now()
	w.lastContact["gone"] = time.Now()
	first.cancel()
	delete(w.running, "gone")
	w.stopping["gone"] = first.done
	w.startLoop(ctx, "gone")
	second := w.running["gone"]
	w.mu.Unlock()
	select {
	case <-first.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the cancelled loop did not end")
	}
	w.mu.Lock()
	_, conf := w.confirms["gone"]
	_, tick := w.lastTick["gone"]
	_, contact := w.lastContact["gone"]
	w.mu.Unlock()
	if conf || tick || contact {
		t.Errorf("stopped loop left state behind: confirms=%v lastTick=%v lastContact=%v", conf, tick, contact)
	}
	second.cancel()
	<-second.done
}

func TestKubeconfigWriteSignalsInformers(t *testing.T) {
	w, _ := labWatcher(t)
	ctx := t.Context()
	w.Store.OnChange(w.onStoreChange)
	if err := w.Store.PutCluster(ctx, store.ClusterRow{Name: "c", Spec: []byte("x"), State: "ready"}); err != nil {
		t.Fatal(err)
	}
	if err := w.Store.PutClusterSecrets(ctx, "c", store.ClusterSecrets{SecretsBundle: []byte("b"), Talosconfig: []byte("t"), Kubeconfig: []byte("old")}); err != nil {
		t.Fatal(err)
	}
	ch := w.kubeSignal("c")
	other := w.kubeSignal("d")
	if err := w.Store.SetKubeconfig(ctx, "c", []byte("new")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ch:
	default:
		t.Fatal("a stored kubeconfig change must reach the cluster's informers")
	}
	select {
	case <-other:
		t.Error("another cluster's informers must not restart")
	default:
	}
	w.dropKubeSignal("c", ch)
	w.dropKubeSignal("d", other)
}

func TestLoopWaitsForItsPredecessorEvenWhenCancelled(t *testing.T) {
	w, _ := labWatcher(t)
	w.SetInterval(time.Hour)
	prev := make(chan struct{})
	w.mu.Lock()
	w.stopping["c"] = prev
	w.startLoop(t.Context(), "c")
	l := w.running["c"]
	w.mu.Unlock()
	l.cancel()
	select {
	case <-l.done:
		t.Fatal("a cancelled loop finished before the loop it waits for")
	case <-time.After(100 * time.Millisecond):
	}
	close(prev)
	select {
	case <-l.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the cancelled loop did not end after its predecessor")
	}
}

const unreachableKubeconfig = `apiVersion: v1
kind: Config
clusters:
- name: c
  cluster: {server: "https://127.0.0.1:1", insecure-skip-tls-verify: true}
users:
- name: u
  user: {token: t}
contexts:
- name: c
  context: {cluster: c, user: u}
current-context: c
`

func TestUnreachableAPIStopsTheInformers(t *testing.T) {
	w, _ := labWatcher(t)
	kc, err := k8s.New([]byte(unreachableKubeconfig))
	if err != nil {
		t.Fatal(err)
	}
	w.mu.Lock()
	w.last["c"] = &cluster.Status{State: cluster.StateReady, APIReachable: true}
	w.mu.Unlock()
	changed := w.kubeSignal("c")
	defer w.dropKubeSignal("c", changed)
	result := make(chan bool, 1)
	go func() { result <- w.runInformers(t.Context(), "c", kc, nil, changed, func(string) {}) }()
	w.signalKube("c")
	select {
	case <-result:
		t.Fatal("informers stopped while the API was reachable")
	case <-time.After(200 * time.Millisecond):
	}
	w.mu.Lock()
	w.last["c"] = &cluster.Status{State: cluster.StateReady}
	w.mu.Unlock()
	w.signalKube("c")
	select {
	case rotated := <-result:
		if rotated {
			t.Error("an unreachable API is not a kubeconfig rotation")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("informers kept serving after the API became unreachable")
	}
}

func TestServiceRefreshOnlyOnChange(t *testing.T) {
	a := &cluster.ServiceHealth{CollectedAt: time.Now(), Pods: []cluster.PodHealth{{Namespace: "n", Name: "p", Phase: "Running", AgeSec: 10}}}
	b := &cluster.ServiceHealth{CollectedAt: time.Now().Add(time.Minute), Pods: []cluster.PodHealth{{Namespace: "n", Name: "p", Phase: "Running", AgeSec: 70}}}
	if !sameServices(a, b) {
		t.Error("ages and collection time alone must not count as a change")
	}
	c := &cluster.ServiceHealth{Pods: []cluster.PodHealth{{Namespace: "n", Name: "p", Phase: "Pending"}}}
	if sameServices(a, c) || sameServices(nil, a) {
		t.Error("a changed phase or a first collection must count as a change")
	}
	if a.Pods[0].AgeSec != 10 {
		t.Error("comparison modified the collected health")
	}
}

func TestResolvesCoverServiceRecoveries(t *testing.T) {
	for rec, alert := range serviceResolves {
		if resolves[rec] != alert {
			t.Errorf("%s resolves %q, want %q", rec, resolves[rec], alert)
		}
	}
	if resolves["api.back"] != "api.unreachable" {
		t.Error("cluster recoveries missing")
	}
}
