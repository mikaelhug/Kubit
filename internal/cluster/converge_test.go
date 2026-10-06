package cluster

import (
	"context"
	"crypto/sha256"
	"strings"
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
	m.kube = map[string]kubeEntry{"lab": {sum: sha256.Sum256(kubeconfig), kc: &k8s.Client{Interface: fake.NewClientset()}}}
	d, _ := config.Parse([]byte(convergeSpec))
	desired := &Desired{Cluster: d, Kubeconfig: kubeconfig}
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
