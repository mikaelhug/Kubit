package watch

import (
	"testing"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/k8s"
	"github.com/mikael/kubit/internal/store"
)

func TestKubeconfigChangeRestartsOnlyThatClustersInformers(t *testing.T) {
	w := testWatcher(t)
	w.Store.OnChange(w.onStoreChange)
	w.Store.PutCluster(store.ClusterRow{Name: "c", Spec: &config.Cluster{}, State: "ready"})
	w.Store.PutClusterSecrets("c", store.ClusterSecrets{Talosconfig: []byte("t"), Kubeconfig: []byte("old")})
	ch := w.kubeSignal("c")
	other := w.kubeSignal("d")
	w.Store.PutClusterSecrets("c", store.ClusterSecrets{Talosconfig: []byte("t"), Kubeconfig: []byte("new")})
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

func TestUnreachableAPIStopsServingStaleInformerReads(t *testing.T) {
	w := testWatcher(t)
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
