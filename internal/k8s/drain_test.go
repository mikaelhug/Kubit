package k8s

import (
	"bytes"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/kubernetes/fake"
)

func bareNodeAndPod() *Client {
	cs := fake.NewClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "w-01"}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "debug", Namespace: "default"}, Spec: corev1.PodSpec{NodeName: "w-01"}, Status: corev1.PodStatus{Phase: corev1.PodRunning}},
	)
	cs.Discovery().(*fakediscovery.FakeDiscovery).Resources = []*metav1.APIResourceList{{GroupVersion: "v1", APIResources: []metav1.APIResource{{Name: "pods", Kind: "Pod"}}}}
	return &Client{Interface: cs}
}

func TestDrainAllEvictsUnmanagedPods(t *testing.T) {
	kc := bareNodeAndPod()
	if err := kc.Drain(t.Context(), "w-01", 10*time.Second, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "debug") {
		t.Errorf("the strict drain must refuse an unmanaged pod: %v", err)
	}
	kc = bareNodeAndPod()
	var log bytes.Buffer
	if err := kc.DrainAll(t.Context(), "w-01", 10*time.Second, &log); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "default/debug") {
		t.Errorf("the unmanaged pod must be named in the log: %q", log.String())
	}
	if pods, _ := kc.CoreV1().Pods("default").List(t.Context(), metav1.ListOptions{}); len(pods.Items) != 0 {
		t.Errorf("the unmanaged pod must be gone: %d left", len(pods.Items))
	}
}

func TestDrainHelperForcesOnlyTheUpgradeDrain(t *testing.T) {
	if h := drainHelper(t.Context(), nil, time.Minute, nil, false); h.Force || !h.IgnoreAllDaemonSets || !h.DeleteEmptyDirData {
		t.Errorf("strict drain: %+v", h)
	}
	if h := drainHelper(t.Context(), nil, time.Minute, nil, true); !h.Force || h.Timeout != time.Minute {
		t.Errorf("upgrade drain: %+v", h)
	}
}

func TestBudgetsBlockingNamesTheBudgetAndPod(t *testing.T) {
	pod := func(ns, name string, labels map[string]string) corev1.Pod {
		return corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Labels: labels}}
	}
	pdb := func(ns, name string, allowed int32, app string) policyv1.PodDisruptionBudget {
		return policyv1.PodDisruptionBudget{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
			Spec:       policyv1.PodDisruptionBudgetSpec{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": app}}},
			Status:     policyv1.PodDisruptionBudgetStatus{DisruptionsAllowed: allowed},
		}
	}
	pods := []corev1.Pod{pod("db", "pg-0", map[string]string{"app": "pg"}), pod("web", "web-1", map[string]string{"app": "web"})}
	got := budgetsBlocking(pods, []policyv1.PodDisruptionBudget{
		pdb("db", "pg", 0, "pg"),
		pdb("web", "web", 1, "web"),
		pdb("other", "pg", 0, "pg"),
	})
	if len(got) != 1 || got[0] != "db/pg (pod pg-0)" {
		t.Errorf("only the exhausted budget over a pod on the node blocks: %v", got)
	}
}

func TestCordonMarkerFollowsTheCordon(t *testing.T) {
	kc := &Client{Interface: fake.NewClientset(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "w-01"}})}
	ctx := t.Context()
	if err := kc.MarkCordon(ctx, "w-01", "upgrade"); err != nil {
		t.Fatal(err)
	}
	if err := kc.Drain(ctx, "w-01", time.Second, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if u, by, err := kc.CordonState(ctx, "w-01"); err != nil || !u || by != "upgrade" {
		t.Errorf("marked and cordoned: %v %q %v", u, by, err)
	}
	if err := kc.Uncordon(ctx, "w-01"); err != nil {
		t.Fatal(err)
	}
	if u, by, err := kc.CordonState(ctx, "w-01"); err != nil || u || by != "" {
		t.Errorf("uncordon must drop the marker: %v %q %v", u, by, err)
	}
	if err := kc.MarkCordon(ctx, "w-01", "upgrade"); err != nil {
		t.Fatal(err)
	}
	if err := kc.Cordon(ctx, "w-01"); err != nil {
		t.Fatal(err)
	}
	if u, by, _ := kc.CordonState(ctx, "w-01"); !u || by != "" {
		t.Errorf("a manual cordon belongs to the user: %v %q", u, by)
	}
}
