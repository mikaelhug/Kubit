package watch

import (
	"testing"
	"time"

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
