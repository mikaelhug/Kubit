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
	"go.yaml.in/yaml/v4"
	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestConfigDiffReadsTheDryRun(t *testing.T) {
	same := "Dry run summary:\nApplied configuration without a reboot (skipped in dry-run).\nConfig diff:\n\nNo changes."
	if d := configDiff(same); d != "" {
		t.Errorf("no changes read as %q", d)
	}
	changed := "Dry run summary:\nApplied configuration without a reboot (skipped in dry-run).\nConfig diff:\n\n--- a\n+++ b\n-  hostname: a\n+  hostname: b"
	if d := configDiff(changed); !strings.HasPrefix(d, "--- a") || !strings.Contains(d, "+  hostname: b") {
		t.Errorf("diff read as %q", d)
	}
}

func TestLowestVersion(t *testing.T) {
	if got := lowest([]string{"v1.14.2", "1.14.0", "v1.15.0", "bogus"}); got != "v1.14.0" {
		t.Errorf("lowest %s", got)
	}
	if got := lowest(nil); got != "" {
		t.Errorf("empty %s", got)
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

func TestAppliedSpecFollowsTheLiveCluster(t *testing.T) {
	d, err := config.Parse([]byte(convergeSpec))
	if err != nil {
		t.Fatal(err)
	}
	ls := &liveState{exists: true, api: true,
		nodes: map[string]*liveNode{
			"cp-01": {member: true, addr: "10.0.0.10", image: nodeImage{version: "v1.14.0", schematic: "old"}},
			"w-01":  {member: true, addr: "10.0.0.11", image: nodeImage{version: "v1.14.2", schematic: "old"}},
			"w-02":  {maint: true, addr: "10.0.0.12"},
		},
		kube: map[string]k8s.NodeStatus{
			"cp-01": {Name: "cp-01", KubeletVersion: "v1.35.1"},
			"w-01":  {Name: "w-01", KubeletVersion: "v1.34.2"},
			"w-09":  {Name: "w-09", InternalIP: "10.0.0.19", Labels: map[string]string{"kubernetes.io/arch": "arm64"}},
		},
	}
	a := appliedSpec(d, ls, nil)
	var hosts []string
	for _, n := range a.Spec.Nodes {
		hosts = append(hosts, n.Hostname+"@"+n.IP)
	}
	if got := strings.Join(hosts, ","); got != "cp-01@10.0.0.10,w-01@10.0.0.11,w-09@10.0.0.19" {
		t.Errorf("nodes %s", got)
	}
	if a.Spec.TalosVersion != "v1.14.0" || a.Spec.KubernetesVersion != "v1.34.2" || a.Spec.SchematicID != "old" {
		t.Errorf("versions %s %s %s", a.Spec.TalosVersion, a.Spec.KubernetesVersion, a.Spec.SchematicID)
	}
	if w := a.Spec.Nodes[2]; w.Role != config.RoleWorker || w.Arch != config.ArchARM64 {
		t.Errorf("undeclared member %+v", w)
	}
	if d.Spec.TalosVersion != "v1.14.2" || len(d.Spec.Nodes) != 3 || d.Spec.Nodes[0].IP != "10.0.0.50" {
		t.Error("the declaration must stay untouched")
	}
}

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

func TestPlanNeedsEveryNodeInMaintenanceToCreate(t *testing.T) {
	spec := strings.ReplaceAll(convergeSpec, "10.0.0.", "127.0.0.")
	d, err := config.Parse([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(testStore(t), t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p, err := m.Plan(ctx, &Desired{Cluster: d, Bundle: bundle(t), Talosconfig: []byte("x"), Kubeconfig: kubeconfigFor("https://127.0.0.1:1")}, ConvergeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Problems) != 1 || !strings.Contains(p.Problems[0], "not in maintenance mode") || len(p.Changes) != 0 {
		t.Errorf("plan %+v", p)
	}
}

func TestPlanRefusesACachedClusterWithOtherSecrets(t *testing.T) {
	d, err := config.Parse([]byte(convergeSpec))
	if err != nil {
		t.Fatal(err)
	}
	st := testStore(t)
	raw, _ := marshalBundle(bundle(t))
	if err := st.PutCluster(context.Background(), store.ClusterRow{Name: "lab", Spec: []byte(convergeSpec), State: StateReady}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutClusterSecrets(context.Background(), "lab", store.ClusterSecrets{SecretsBundle: raw}); err != nil {
		t.Fatal(err)
	}
	m := NewManager(st, t.TempDir())
	if _, err := m.Plan(context.Background(), &Desired{Cluster: d, Bundle: bundle(t)}, ConvergeOptions{}); err == nil || !strings.Contains(err.Error(), "other secrets") {
		t.Errorf("plan over another cluster's cache: %v", err)
	}
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
	m := NewManager(testStore(t), t.TempDir())
	kubeconfig := kubeconfigFor("https://10.0.0.10:6443")
	cs := fake.NewClientset()
	m.kube = map[string]kubeEntry{"lab": {sum: sha256.Sum256(kubeconfig), kc: &k8s.Client{Interface: cs}}}
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
	stale := metav1.NewMicroTime(time.Now().Add(-2 * applyLeaseTime))
	secs, holder := int32(60), "crashed"
	if _, err := cs.CoordinationV1().Leases("kube-system").Create(ctx, &coordinationv1.Lease{ObjectMeta: metav1.ObjectMeta{Name: applyLease}, Spec: coordinationv1.LeaseSpec{HolderIdentity: &holder, LeaseDurationSeconds: &secs, AcquireTime: &stale, RenewTime: &stale}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.LockApply(ctx, desired, "ci-3"); err != nil {
		t.Errorf("an expired lease must be taken over: %v", err)
	}
}

func marshalBundle(b *secrets.Bundle) ([]byte, error) { return yaml.Marshal(b) }
