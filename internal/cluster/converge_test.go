package cluster

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/k8s"
	"github.com/mikael/kubit/internal/store"
	talosconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"k8s.io/client-go/kubernetes/fake"
)

func TestConfigDiffReadsTalosDryRunOutput(t *testing.T) {
	same := "Dry run summary:\nApplied configuration without a reboot (skipped in dry-run).\nConfig diff:\n\nNo changes."
	if d := configDiff(same); d != "" {
		t.Errorf("no changes read as %q", d)
	}
	changed := "Dry run summary:\nApplied configuration without a reboot (skipped in dry-run).\nConfig diff:\n\n--- a\n+++ b\n-  hostname: a\n+  hostname: b"
	if d := configDiff(changed); !strings.HasPrefix(d, "--- a") || !strings.Contains(d, "+  hostname: b") {
		t.Errorf("diff read as %q", d)
	}
}

const convergeSpec = `apiVersion: kubit.dev/v1
kind: Cluster
metadata: {name: lab}
spec:
  talosVersion: v1.14.2
  kubernetesVersion: v1.35.1
  extensions: [siderolabs/gvisor]
  controlPlane: {endpoint: "https://10.0.0.10:6443"}
  nodes:
    - {hostname: cp-01, ip: 10.0.0.50, role: controlplane, installDisk: {path: /dev/sda}, network: {addresses: [10.0.0.10/24], gateway: 10.0.0.1, nameservers: [10.0.0.1]}}
    - {hostname: w-01, ip: 10.0.0.11, role: worker, installDisk: {path: /dev/sda}}
    - {hostname: w-02, ip: 10.0.0.12, role: worker, installDisk: {path: /dev/sda}}
`

func bundle(t *testing.T) *secrets.Bundle {
	t.Helper()
	contract, err := talosconfig.ParseContractFromVersion(config.MinTalosVersion)
	if err != nil {
		t.Fatal(err)
	}
	b, err := secrets.NewBundle(secrets.NewFixedClock(time.Now()), contract)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestConvergeRefusesRemovalWithoutPermission(t *testing.T) {
	m := NewManager(testStore(t), t.TempDir())
	p := &Plan{Cluster: "lab", Changes: []Change{{Action: ActRemove, Target: "w-09", Blocked: "needs --allow-removal"}}}
	d, _ := config.Parse([]byte(convergeSpec))
	if err := m.Converge(context.Background(), &Desired{Cluster: d}, p, ConvergeOptions{}, func(Event) {}); err == nil || !strings.Contains(err.Error(), "--allow-removal") {
		t.Errorf("converge: %v", err)
	}
}

func TestApplyLeaseSerialisesApplies(t *testing.T) {
	st := testStore(t)
	m := NewManager(st, t.TempDir())
	kubeconfig := kubeconfigFor("https://10.0.0.10:6443")
	st.PutCluster(store.ClusterRow{Name: "lab", Spec: parsedSpec(t, convergeSpec), State: StateReady})
	st.PutClusterSecrets("lab", store.ClusterSecrets{Talosconfig: []byte("t"), Kubeconfig: kubeconfig})
	d, _ := config.Parse([]byte(convergeSpec))
	desired := &Desired{Cluster: d, Kubeconfig: kubeconfig}
	m.kube = map[string]kubeEntry{"lab": {sum: sha256.Sum256(m.liveKubeconfig(desired)), kc: &k8s.Client{Interface: fake.NewClientset()}}}
	ctx := context.Background()
	unlock, err := m.LockApply(ctx, desired, "ci-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.LockApply(ctx, desired, "ci-2"); err == nil || !strings.Contains(err.Error(), "ci-1") {
		t.Errorf("a second apply must wait: %v", err)
	}
	unlock()
	unlock2, err := m.LockApply(ctx, desired, "ci-2")
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	unlock2()
}

func TestApplyConfigsReachesTheMovedEndpointAfterAReload(t *testing.T) {
	// Reproduces the e2e failure: after a control-plane move, the config step dialed the old API address.
	var oldHits, newHits atomic.Int32
	serve := func(hits *atomic.Int32) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"kind":"NodeList","apiVersion":"v1","items":[]}`))
		}))
		t.Cleanup(srv.Close)
		return srv
	}
	before, after := serve(&oldHits), serve(&newHits)
	m := NewManager(testStore(t), t.TempDir())
	b := bundle(t)
	m.use(&Desired{Cluster: parsedSpec(t, convergeSpec), Bundle: b, Kubeconfig: kubeconfigFor(before.URL)})
	edited := parsedSpec(t, strings.Replace(convergeSpec, "https://10.0.0.10:6443", "https://10.0.0.51:6443", 1))
	m.use(&Desired{Cluster: edited, Bundle: b, Kubeconfig: kubeconfigFor(after.URL)})
	m.pinEndpoint("lab", after.URL)
	for i := range edited.Spec.Nodes {
		edited.Spec.Nodes[i].InstallDisk = config.InstallDisk{}
	}
	_ = m.ApplyConfigs(context.Background(), edited, "", func(Event) {})
	if oldHits.Load() != 0 || newHits.Load() == 0 {
		t.Errorf("the config step must use the live endpoint: old %d, new %d requests", oldHits.Load(), newHits.Load())
	}
}
