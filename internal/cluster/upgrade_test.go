package cluster

import (
	"context"
	"crypto/sha256"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/k8s"
	"github.com/mikael/kubit/internal/store"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestAChangedSchematicReimagesAtTheSameVersion(t *testing.T) {
	want := nodeImage{version: "v1.14.1", schematic: "new"}
	for _, tc := range []struct {
		name    string
		cur     nodeImage
		reimage bool
		ok      bool
	}{
		{"same version and schematic", nodeImage{"v1.14.1", "new"}, true, true},
		{"old schematic", nodeImage{"v1.14.1", "old"}, false, false},
		{"unknown schematic, image changes", nodeImage{"v1.14.1", ""}, true, false},
	} {
		if got := tc.cur.satisfies(want, tc.reimage); got != tc.ok {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.ok)
		}
	}
}

func TestKubernetesUpgradeCannotSkipAMinorOrGoBack(t *testing.T) {
	for _, tc := range []struct {
		from, to string
		fail     string
	}{
		{"v1.34.1", "v1.35.0", ""},
		{"v1.34.1", "v1.36.0", "skips a minor"},
		{"v1.34.1", "v1.34.0", "older than"},
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

func TestUpgradePrecheckFollowsTheTalosKubernetesSupportMatrix(t *testing.T) {
	if known, err := kubernetesSupported("v1.14.1", "v1.34.1"); !known || err != nil {
		t.Errorf("Talos 1.14 runs Kubernetes 1.34: %v %v", known, err)
	}
	if _, err := kubernetesSupported("v1.14.1", "v1.31.0"); err == nil || !strings.Contains(err.Error(), "too old") {
		t.Errorf("Kubernetes 1.31 on Talos 1.14: %v", err)
	}
	if _, err := kubernetesSupported("v1.14.1", "v1.38.0"); err == nil || !strings.Contains(err.Error(), "too new") {
		t.Errorf("Kubernetes 1.38 on Talos 1.14: %v", err)
	}
	c := &config.Cluster{}
	c.Spec.TalosVersion, c.Spec.KubernetesVersion = "v1.14.1", "v1.34.1"
	if err := checkUpgradeVersions(c, "talos", "v1.31.0", "v1.14.1", func(Event) {}); err == nil {
		t.Error("a Talos that cannot run the current Kubernetes must fail the precheck")
	}
}

func kubeletNode(name, version string) *corev1.Node {
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}, Status: corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{KubeletVersion: version}}}
}

func TestAStragglingKubeletKeepsTheKubernetesUpgradeGoing(t *testing.T) {
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
	return store.New()
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

func TestRemoveRefusesTheEndpointHost(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	st.PutCluster(store.ClusterRow{Name: "lab", Spec: parsedSpec(t, threeControlPlanes), State: StateReady})
	m := NewManager(st, t.TempDir())
	err := m.RemoveNode(ctx, "lab", "cp-01", func(Event) {})
	if err == nil || !strings.Contains(err.Error(), "API endpoint") {
		t.Errorf("removing the endpoint host: %v", err)
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

func loopbackManager(t *testing.T, spec string) *Manager {
	t.Helper()
	st := testStore(t)
	st.PutCluster(store.ClusterRow{Name: "lab", Spec: parsedSpec(t, spec), State: StateReady})
	kubeconfig := []byte("kubeconfig")
	m := NewManager(st, t.TempDir())
	m.use(&Desired{Cluster: parsedSpec(t, spec), Bundle: bundle(t), Talosconfig: talosconfigFor(t), Kubeconfig: kubeconfig})
	m.kube = map[string]kubeEntry{"lab": {sum: sha256.Sum256(kubeconfig), kc: &k8s.Client{Interface: fake.NewClientset()}}}
	return m
}

func TestAddNodeRefusesAnExistingMember(t *testing.T) {
	m := loopbackManager(t, loopbackCluster)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := m.AddNode(ctx, "lab", config.Node{Hostname: "cp-01", IP: "10.0.0.8"}, func(Event) {}); err == nil || !strings.Contains(err.Error(), "already in cluster") {
		t.Errorf("a member must not join twice: %v", err)
	}
}

func TestUpgradeReleasesKubitsCordonButNeverTheUsers(t *testing.T) {
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
}

func TestEtcdToleranceFollowsTalosUpgradeRules(t *testing.T) {
	healthy := func(name string) etcdMemberHealth { return etcdMemberHealth{id: 1, name: name, healthy: true} }
	for _, tc := range []struct {
		name    string
		members []etcdMemberHealth
		alarms  []string
		fail    string
	}{
		{"three healthy", []etcdMemberHealth{healthy("cp-01"), healthy("cp-02"), healthy("cp-03")}, nil, ""},
		{"two members", []etcdMemberHealth{healthy("cp-01"), healthy("cp-02")}, nil, "2 members"},
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
