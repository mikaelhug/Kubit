package cluster

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/mikael/kubit/internal/store"
)

func kubeconfigFor(server string) []byte {
	return fmt.Appendf(nil, `apiVersion: v1
kind: Config
clusters:
- name: c
  cluster:
    server: %s
contexts:
- name: c
  context:
    cluster: c
    user: u
current-context: c
users:
- name: u
  user:
    token: t
`, server)
}

func TestKubeClientIsCachedPerKubeconfig(t *testing.T) {
	c, _ := store.NewCrypto(bytes.Repeat([]byte{5}, 32))
	st, err := store.Open(t.TempDir(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	m := NewManager(st, t.TempDir())
	if err := st.PutCluster(ctx, store.ClusterRow{Name: "lab", Spec: []byte("x"), State: StateReady}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutClusterSecrets(ctx, "lab", store.ClusterSecrets{SecretsBundle: []byte("b"), Talosconfig: []byte("t"), Kubeconfig: kubeconfigFor("https://10.0.0.1:6443")}); err != nil {
		t.Fatal(err)
	}
	first, err := m.KubeClient(ctx, "lab")
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := m.KubeClient(ctx, "lab"); again != first {
		t.Error("an unchanged kubeconfig must reuse the client")
	}
	if err := st.SetKubeconfig(ctx, "lab", kubeconfigFor("https://10.0.0.2:6443")); err != nil {
		t.Fatal(err)
	}
	rotated, err := m.KubeClient(ctx, "lab")
	if err != nil {
		t.Fatal(err)
	}
	if rotated == first {
		t.Error("a new kubeconfig must replace the client")
	}
	if again, _ := m.KubeClient(ctx, "lab"); again != rotated {
		t.Error("the replacement must be cached")
	}
	if err := st.DeleteCluster(ctx, "lab"); err != nil {
		t.Fatal(err)
	}
	m.kubeMu.Lock()
	_, kept := m.kube["lab"]
	m.kubeMu.Unlock()
	if kept {
		t.Error("forgetting the cluster must drop its client")
	}
	if _, err := m.KubeClient(ctx, "lab"); err == nil {
		t.Error("a forgotten cluster has no client")
	}
}
