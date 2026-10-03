package cluster

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/k8s"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestNodeImageSatisfies(t *testing.T) {
	want := nodeImage{version: "v1.14.1", schematic: "new"}
	for _, tc := range []struct {
		name    string
		cur     nodeImage
		reimage bool
		ok      bool
	}{
		{"same version and schematic", nodeImage{"v1.14.1", "new"}, true, true},
		{"old schematic", nodeImage{"v1.14.1", "old"}, false, false},
		{"old version", nodeImage{"v1.14.0", "new"}, false, false},
		{"unknown schematic, image unchanged", nodeImage{"v1.14.1", ""}, false, true},
		{"unknown schematic, image changes", nodeImage{"v1.14.1", ""}, true, false},
	} {
		if got := tc.cur.satisfies(want, tc.reimage); got != tc.ok {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.ok)
		}
	}
}

func TestKubernetesUpgradeTarget(t *testing.T) {
	for _, tc := range []struct {
		from, to string
		fail     string
	}{
		{"v1.34.1", "v1.34.2", ""},
		{"v1.34.1", "v1.35.0", ""},
		{"v1.34.1", "v1.36.0", "skips a minor"},
		{"v1.34.1", "v1.34.0", "older than"},
		{"v1.34.1", "v1.33.9", "older than"},
		{"v1.34.1", "v2.0.0", "skips a minor"},
		{"v1.34.1", "v1.35", "not a Kubernetes version"},
		{"v1.34.1", "vlatest", "not a Kubernetes version"},
	} {
		err := checkKubernetesStep(tc.from, tc.to)
		if tc.fail == "" && err != nil {
			t.Errorf("%s → %s: %v", tc.from, tc.to, err)
		}
		if tc.fail != "" && (err == nil || !strings.Contains(err.Error(), tc.fail)) {
			t.Errorf("%s → %s: %v, want %q", tc.from, tc.to, err, tc.fail)
		}
	}
}

func TestTalosKubernetesCompatibility(t *testing.T) {
	if known, err := kubernetesSupported("v1.14.1", "v1.34.1"); !known || err != nil {
		t.Errorf("Talos 1.14 runs Kubernetes 1.34: %v %v", known, err)
	}
	if _, err := kubernetesSupported("v1.14.1", "v1.31.0"); err == nil || !strings.Contains(err.Error(), "too old") {
		t.Errorf("Kubernetes 1.31 on Talos 1.14: %v", err)
	}
	if _, err := kubernetesSupported("v1.14.1", "v1.38.0"); err == nil || !strings.Contains(err.Error(), "too new") {
		t.Errorf("Kubernetes 1.38 on Talos 1.14: %v", err)
	}
	if known, err := kubernetesSupported("v1.14.99", "v1.38.0"); known || err != nil {
		t.Errorf("a Talos patch newer than the machinery only warns: %v %v", known, err)
	}
	if known, err := kubernetesSupported("v1.99.0", "v1.34.1"); known || err != nil {
		t.Errorf("a Talos newer than the machinery is unchecked, not rejected: %v %v", known, err)
	}
	c := &config.Cluster{}
	c.Spec.TalosVersion, c.Spec.KubernetesVersion = "v1.14.1", "v1.34.1"
	var warned bool
	sink := Sink(func(e Event) { warned = warned || e.Level == Warn })
	if err := checkUpgradeVersions(c, "kubernetes", "v1.34.1", "v1.36.0", sink); err == nil {
		t.Error("a skipped minor must fail the precheck")
	}
	if err := checkUpgradeVersions(c, "talos", "v1.31.0", "v1.14.1", sink); err == nil {
		t.Error("a Talos that cannot run the current Kubernetes must fail the precheck")
	}
	if err := checkUpgradeVersions(c, "talos", "v1.34.1", "v1.99.0", sink); err != nil || !warned {
		t.Errorf("unknown Talos: %v warned=%v", err, warned)
	}
}

func kubeletNode(name, version string) *corev1.Node {
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}, Status: corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{KubeletVersion: version}}}
}

func TestLiveKubeletsDecideWhetherAnUpgradeIsDone(t *testing.T) {
	c := cl(node("cp-01", "", "10.0.0.1"), node("cp-02", "", "10.0.0.2"))
	c.Spec.KubernetesVersion = "v1.35.0"
	kc := &k8s.Client{Interface: fake.NewClientset(kubeletNode("cp-01", "v1.35.0"), kubeletNode("cp-02", "v1.34.2"))}
	lowest, rolled := liveKubelets(context.Background(), kc, c, "v1.35.0")
	if rolled || lowest != "v1.34.2" {
		t.Errorf("a straggler on v1.34.2 must keep the upgrade going from it: %s %v", lowest, rolled)
	}
	kc = &k8s.Client{Interface: fake.NewClientset(kubeletNode("cp-01", "v1.35.0"), kubeletNode("cp-02", "v1.35.0"))}
	if _, rolled := liveKubelets(context.Background(), kc, c, "v1.35.0"); !rolled {
		t.Error("every kubelet on the target is done")
	}
}

func testStore(t *testing.T) *store.Store {
	t.Helper()
	crypto, _ := store.NewCrypto(bytes.Repeat([]byte{7}, 32))
	st, err := store.Open(t.TempDir(), crypto)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

const threeControlPlanes = `
apiVersion: kubit.dev/v1
kind: Cluster
metadata: { name: lab }
spec:
  nodes:
    - { hostname: cp-01, ip: 10.0.0.1, role: controlplane, installDisk: { path: /dev/sda } }
    - { hostname: cp-02, ip: 10.0.0.2, role: controlplane, installDisk: { path: /dev/sda } }
    - { hostname: cp-03, ip: 10.0.0.3, role: controlplane, installDisk: { path: /dev/sda } }
`

func TestRemoveRefusesTheEndpointHostEvenWithForce(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	if err := st.PutCluster(ctx, store.ClusterRow{Name: "lab", Spec: []byte(threeControlPlanes), State: StateReady}); err != nil {
		t.Fatal(err)
	}
	m := NewManager(st, t.TempDir())
	err := m.RemoveNode(ctx, "lab", "cp-01", RemoveOptions{Force: true}, func(Event) {})
	if err == nil || !strings.Contains(err.Error(), "API endpoint") {
		t.Errorf("removing the endpoint host: %v", err)
	}
}

func TestApplyPlanRejectsAPlanOfAnotherSpec(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	if err := st.PutCluster(ctx, store.ClusterRow{Name: "lab", Spec: []byte(threeControlPlanes), State: StateReady}); err != nil {
		t.Fatal(err)
	}
	m := NewManager(st, t.TempDir())
	err := m.ApplyPlan(ctx, "lab", specHash([]byte("an older spec")), func(Event) {})
	if !errors.Is(err, errStalePlan) {
		t.Errorf("a plan of another spec must be stale: %v", err)
	}
	err = m.ApplyPlan(ctx, "lab", specHash([]byte(threeControlPlanes)), func(Event) {})
	if errors.Is(err, errStalePlan) {
		t.Error("a plan of the current spec is not stale")
	}
	if err := st.SetClusterState(ctx, "lab", StateBootstrapped); err != nil {
		t.Fatal(err)
	}
	if err := m.ApplyPlan(ctx, "lab", specHash([]byte(threeControlPlanes)), func(Event) {}); errors.Is(err, errStalePlan) {
		t.Error("a state change alone must not make the plan stale")
	}
}

func TestAddNodeResumesOnlyAnUnfinishedIdenticalNode(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	m := NewManager(st, t.TempDir())
	n := config.Node{Hostname: "w-01", IP: "10.0.0.9", MAC: "52:54:00:00:00:09"}
	if !sameDeclaredNode(n, config.Node{Hostname: "w-01", IP: "10.0.0.9", MAC: "52:54:00:00:00:09"}) {
		t.Error("the same hostname, address and MAC is the same node")
	}
	if sameDeclaredNode(n, config.Node{Hostname: "w-01", IP: "10.0.0.10", MAC: "52:54:00:00:00:09"}) {
		t.Error("another address is another node")
	}
	for state, resume := range map[string]bool{NodeDiscovered: true, NodeInstalling: true, NodeFailed: true, NodeJoined: false, NodeReady: false} {
		if err := st.UpsertNode(ctx, store.NodeRow{IP: n.IP, MAC: n.MAC, Hostname: n.Hostname, State: state, Source: "manual"}); err != nil {
			t.Fatal(err)
		}
		if err := st.SetNodeState(ctx, n.IP, state); err != nil {
			t.Fatal(err)
		}
		if got := m.addUnfinished(ctx, n.IP); got != resume {
			t.Errorf("%s: resume=%v, want %v", state, got, resume)
		}
	}
}

func TestRefusedSpecChangesAreInvalidInput(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	m := NewManager(st, t.TempDir())
	c, err := config.Parse([]byte(threeControlPlanes + "  storage: { systemDisk: true }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SaveCluster(ctx, c, StateReady); err != nil {
		t.Fatal(err)
	}
	if err := m.recordNode(ctx, c, c.Spec.Nodes[0], NodeReady, []byte("cfg")); err != nil {
		t.Fatal(err)
	}
	if err := st.PutNodeMachineConfig(ctx, "10.0.0.1", []byte("cfg"), true); err != nil {
		t.Fatal(err)
	}
	c.Spec.Storage.EphemeralSize = "20GiB"
	err = m.SaveCluster(ctx, c, "")
	if !errors.Is(err, store.ErrInvalid) || !strings.HasPrefix(err.Error(), "storage must stay") {
		t.Errorf("a refused storage change: %v", err)
	}
}

const loopbackCluster = `
apiVersion: kubit.dev/v1
kind: Cluster
metadata: { name: lab }
spec:
  kubernetesVersion: v1.34.2
  nodes:
    - { hostname: cp-01, ip: 127.0.0.1, role: controlplane, installDisk: { path: /dev/sda } }
`

func loopbackManager(t *testing.T, spec string, nodes ...*corev1.Node) (*Manager, *store.Store) {
	t.Helper()
	tcs, err := talosconfigs()
	if err != nil {
		t.Fatal(err)
	}
	st := testStore(t)
	ctx := context.Background()
	if err := st.PutCluster(ctx, store.ClusterRow{Name: "lab", Spec: []byte(spec), State: StateReady}); err != nil {
		t.Fatal(err)
	}
	kubeconfig := []byte("kubeconfig")
	if err := st.PutClusterSecrets(ctx, "lab", store.ClusterSecrets{SecretsBundle: []byte("{}"), Talosconfig: tcs[0], Kubeconfig: kubeconfig}); err != nil {
		t.Fatal(err)
	}
	m := NewManager(st, t.TempDir())
	objects := make([]runtime.Object, len(nodes))
	for i, n := range nodes {
		objects[i] = n
	}
	m.kube = map[string]kubeEntry{"lab": {sum: sha256.Sum256(kubeconfig), kc: &k8s.Client{Interface: fake.NewClientset(objects...)}}}
	return m, st
}

func TestRolledKubeletsRecordTheKubernetesVersion(t *testing.T) {
	m, _ := loopbackManager(t, loopbackCluster, kubeletNode("cp-01", "v1.35.0"))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = m.UpgradeKubernetes(ctx, "lab", "v1.35.0", func(Event) {})
	c, _, err := m.LoadCluster(context.Background(), "lab")
	if err != nil {
		t.Fatal(err)
	}
	if c.Spec.KubernetesVersion != "v1.35.0" {
		t.Errorf("every kubelet on the target must record it: %s", c.Spec.KubernetesVersion)
	}

}

func TestSchematicDiffersFallsBackToTheDeclaredImage(t *testing.T) {
	for _, tc := range []struct {
		name              string
		cur               nodeImage
		desired, declared string
		reimage           bool
	}{
		{"installed matches", nodeImage{"v1.14.1", "gpu"}, "gpu", "base", false},
		{"installed differs", nodeImage{"v1.14.1", "base"}, "gpu", "gpu", true},
		{"unknown, same declared image", nodeImage{"v1.14.1", ""}, "base", "base", false},
		{"unknown, other declared image", nodeImage{}, "gpu", "base", true},
	} {
		if got := tc.cur.schematicDiffers(tc.desired, tc.declared); got != tc.reimage {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.reimage)
		}
	}
}

func TestResumedAddNodeValidatesTheResubmittedNode(t *testing.T) {
	spec := loopbackCluster + "    - { hostname: w-01, ip: 127.0.0.1, mac: \"52:54:00:00:00:09\", role: worker, installDisk: { path: /dev/sda } }\n"
	spec = strings.Replace(spec, "ip: 127.0.0.1, role: controlplane", "ip: 10.0.0.1, role: controlplane", 1)
	m, st := loopbackManager(t, spec)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := st.UpsertNode(ctx, store.NodeRow{IP: "127.0.0.1", Cluster: "lab", MAC: "52:54:00:00:00:09", Hostname: "w-01", Role: "worker", State: NodeFailed, Source: "manual"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetNodeState(ctx, "127.0.0.1", NodeFailed); err != nil {
		t.Fatal(err)
	}
	n := config.Node{Hostname: "w-01", IP: "127.0.0.1", MAC: "52:54:00:00:00:09", Role: config.RoleWorker, Pool: "gpu", InstallDisk: config.InstallDisk{Path: "/dev/sda"}}
	err := m.AddNode(ctx, "lab", n, func(Event) {})
	if err == nil || !strings.Contains(err.Error(), `pool "gpu" is not declared`) {
		t.Errorf("the resubmitted node must replace and revalidate the stored one: %v", err)
	}
}

func TestConfigBehindAppliesOnlyWhenTheDeclarationMoved(t *testing.T) {
	missing := errors.New("machine config for 10.0.0.1: not found")
	for _, tc := range []struct {
		name   string
		stored []byte
		err    error
		want   []byte
		behind bool
	}{
		{"unchanged", []byte("cfg"), nil, []byte("cfg"), false},
		{"regenerated for the new version", []byte("cfg"), nil, []byte("cfg v1.15"), true},
		{"nothing stored", nil, missing, []byte("cfg"), true},
		{"nothing to apply", []byte("cfg"), nil, nil, false},
	} {
		if got := configBehind(tc.stored, tc.err, tc.want); got != tc.behind {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.behind)
		}
	}
}

func TestApplyIfBehindLeavesAnUpToDateNodeAlone(t *testing.T) {
	st := testStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m := NewManager(st, t.TempDir())
	c, err := config.Parse([]byte(threeControlPlanes))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SaveCluster(ctx, c, StateReady); err != nil {
		t.Fatal(err)
	}
	if err := m.recordNode(ctx, c, c.Spec.Nodes[0], NodeReady, []byte("cfg")); err != nil {
		t.Fatal(err)
	}
	if err := st.PutNodeMachineConfig(ctx, "10.0.0.1", []byte("cfg"), false); err != nil {
		t.Fatal(err)
	}
	for _, cfg := range [][]byte{[]byte("cfg"), nil} {
		rebooted, err := m.applyIfBehind(ctx, c.Spec.Nodes[0], cfg, nil, "node:cp-01", func(Event) { t.Error("no event for an unchanged node") })
		if rebooted || err != nil {
			t.Errorf("%q: %v %v", cfg, rebooted, err)
		}
	}
}

func TestUpgradedDeclarationLeavesTheLoadedClusterUntouched(t *testing.T) {
	c, err := config.Parse([]byte(threeControlPlanes + "  talosVersion: v1.14.0\n  schematicID: old\n  pools:\n    - { name: storage, role: worker, extensions: [siderolabs/iscsi-tools], schematicID: old-storage }\n    - { name: plain, role: worker }\n"))
	if err != nil {
		t.Fatal(err)
	}
	schematics := func(c *config.Cluster) map[string]string {
		out := map[string]string{}
		for _, p := range c.Spec.Pools {
			out[p.Name] = p.SchematicID
		}
		return out
	}
	next := upgradedDeclaration(c, "v1.15.0", "new", map[string]string{"storage": "new-storage"})
	if got := schematics(&next); next.Spec.TalosVersion != "v1.15.0" || next.Spec.SchematicID != "new" || got["storage"] != "new-storage" || got["plain"] != "" {
		t.Errorf("next: %s %s %v", next.Spec.TalosVersion, next.Spec.SchematicID, got)
	}
	if got := schematics(c); c.Spec.TalosVersion != "v1.14.0" || c.Spec.SchematicID != "old" || got["storage"] != "old-storage" {
		t.Errorf("the loaded cluster changed: %s %s %v", c.Spec.TalosVersion, c.Spec.SchematicID, got)
	}
}

func TestCordonOfTellsKubitsCordonFromTheUsers(t *testing.T) {
	for _, tc := range []struct {
		unschedulable bool
		by            string
		want          cordonHold
	}{
		{false, "", cordonNone},
		{true, "", cordonUser},
		{true, "upgrade", cordonKubit},
		{false, "upgrade", cordonKubit},
	} {
		if got := cordonOf(tc.unschedulable, tc.by); got != tc.want {
			t.Errorf("%v %q: %v, want %v", tc.unschedulable, tc.by, got, tc.want)
		}
	}
}

func TestKubitCordonIsReleasedOnRetry(t *testing.T) {
	kc := &k8s.Client{Interface: fake.NewClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "w-01"}, Spec: corev1.NodeSpec{Unschedulable: true}},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "w-02"}},
	)}
	ctx := context.Background()
	if hold, err := holdCordon(ctx, kc, "w-01"); err != nil || hold != cordonUser {
		t.Fatalf("a node cordoned before the upgrade is the user's: %v %v", hold, err)
	}
	if hold, err := holdCordon(ctx, kc, "w-02"); err != nil || hold != cordonKubit {
		t.Fatalf("a schedulable node is marked: %v %v", hold, err)
	}
	if err := kc.DrainAll(ctx, "w-02", time.Second, io.Discard); err != nil {
		t.Fatal(err)
	}
	if hold, err := holdCordon(ctx, kc, "w-02"); err != nil || hold != cordonKubit {
		t.Fatalf("a retry must still see Kubit's cordon: %v %v", hold, err)
	}
	if !resumesCordon(ctx, kc, "talos", "w-02") || resumesCordon(ctx, kc, "kubernetes", "w-02") || resumesCordon(ctx, kc, "talos", "w-01") {
		t.Error("only a Talos upgrade passes a node Kubit cordoned")
	}
	for _, name := range []string{"w-01", "w-02"} {
		if err := releaseCordon(ctx, kc, name, "node:"+name, func(Event) {}); err != nil {
			t.Fatal(err)
		}
	}
	if u, _, _ := kc.CordonState(ctx, "w-01"); !u {
		t.Error("the user's cordon must stay")
	}
	if u, by, _ := kc.CordonState(ctx, "w-02"); u || by != "" {
		t.Errorf("Kubit's cordon must be released: %v %q", u, by)
	}
	if _, err := holdCordon(ctx, kc, "w-03"); err == nil {
		t.Error("an unknown node must fail")
	}
}

func TestEtcdToleranceFollowsTalosUpgradeRules(t *testing.T) {
	healthy := func(name string) etcdMemberHealth { return etcdMemberHealth{id: 1, name: name, healthy: true} }
	for _, tc := range []struct {
		name    string
		members []etcdMemberHealth
		alarms  []string
		fail    string
	}{
		{"single member", []etcdMemberHealth{healthy("cp-01")}, nil, ""},
		{"three healthy", []etcdMemberHealth{healthy("cp-01"), healthy("cp-02"), healthy("cp-03")}, nil, ""},
		{"two members", []etcdMemberHealth{healthy("cp-01"), healthy("cp-02")}, nil, "2 members"},
		{"not started", []etcdMemberHealth{healthy("cp-01"), {id: 0xab}, healthy("cp-03")}, nil, "member 00000000000000ab is not started"},
		{"unhealthy", []etcdMemberHealth{healthy("cp-01"), {id: 2, name: "cp-02"}, healthy("cp-03")}, nil, "member cp-02 is not healthy"},
		{"alarm", []etcdMemberHealth{healthy("cp-01")}, []string{"NOSPACE"}, "alarm NOSPACE"},
	} {
		err := etcdTolerance(tc.members, tc.alarms)
		if tc.fail == "" && err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}
		if tc.fail != "" && (err == nil || !strings.Contains(err.Error(), tc.fail)) {
			t.Errorf("%s: %v, want %q", tc.name, err, tc.fail)
		}
	}
}

func TestPeerAddressPrefersTheDeclaredControlPlane(t *testing.T) {
	cps := []config.Node{{Hostname: "cp-01", IP: "10.0.0.1"}}
	if got := peerAddress(talos.EtcdPeer{Hostname: "cp-01", PeerURLs: []string{"https://192.168.9.1:2380"}}, cps); got != "10.0.0.1" {
		t.Errorf("declared: %s", got)
	}
	if got := peerAddress(talos.EtcdPeer{Hostname: "cp-09", PeerURLs: []string{"https://10.0.0.9:2380"}}, cps); got != "10.0.0.9" {
		t.Errorf("undeclared: %s", got)
	}
	if got := peerAddress(talos.EtcdPeer{Hostname: "cp-09"}, cps); got != "" {
		t.Errorf("no address: %s", got)
	}
}

func TestManifestSyncNeedsTheKubernetesAPI(t *testing.T) {
	m, _ := loopbackManager(t, loopbackCluster)
	c, _, err := m.LoadCluster(context.Background(), "lab")
	if err != nil {
		t.Fatal(err)
	}
	cs := fake.NewClientset()
	cs.PrependReactor("list", "nodes", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("connection refused")
	})
	var failed bool
	err = m.syncManifestsStep(context.Background(), c, &k8s.Client{Interface: cs}, func(e Event) {
		failed = failed || (e.Step == manifestsStep.ID && e.Status == StepFailed)
	})
	if err == nil || !strings.Contains(err.Error(), "bootstrap manifests not synced") || !failed {
		t.Errorf("an unreachable API must fail the manifests step: %v %v", err, failed)
	}
}

func TestAddNodePlansTheManifestSync(t *testing.T) {
	m, _ := loopbackManager(t, loopbackCluster)
	var plan []Step
	_ = m.AddNode(context.Background(), "lab", config.Node{Hostname: "cp-01", IP: "10.0.0.9"}, func(e Event) {
		if e.Kind == KindSteps {
			plan = e.Steps
		}
	})
	ids := make([]string, 0, len(plan))
	for _, s := range plan {
		ids = append(ids, s.ID)
	}
	if strings.Join(ids, ",") != "preflight,secrets,firewall,manifests,install,ready" {
		t.Errorf("plan: %v", ids)
	}
}
