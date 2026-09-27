package k8s

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

func deployment(ns, name string) *appsv1.Deployment {
	one := int32(1)
	return &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}, Spec: appsv1.DeploymentSpec{Replicas: &one}}
}

func pod(ns, name, node string, phase corev1.PodPhase, labels map[string]string) *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Labels: labels}, Spec: corev1.PodSpec{NodeName: node}, Status: corev1.PodStatus{Phase: phase}}
}

func names(ws []Workload) []string {
	var out []string
	for _, w := range ws {
		out = append(out, w.Namespace+"/"+w.Name)
	}
	return out
}

func TestReadsFallBackToLiveUntilTheCacheSynced(t *testing.T) {
	ctx := context.Background()
	live := fake.NewClientset(deployment("default", "live"))
	c := &Client{Interface: live}
	got, err := c.Workloads(ctx)
	if err != nil || len(got) != 1 || got[0].Name != "live" {
		t.Fatalf("no cache: %v %v", names(got), err)
	}

	cached := fake.NewClientset(
		deployment("kube-system", "b"), deployment("kube", "a"), deployment("default", "c"),
		pod("apps", "web-1", "n1", corev1.PodRunning, map[string]string{"app": "web"}),
		pod("apps", "web-0", "n2", corev1.PodRunning, map[string]string{"app": "web"}),
		pod("apps", "db-0", "n1", corev1.PodPending, map[string]string{"app": "db"}),
	)
	f := informers.NewSharedInformerFactory(cached, 0)
	k := NewCache(f)
	c.UseCache(k)
	if got, _ := c.Workloads(ctx); len(got) != 1 || got[0].Name != "live" {
		t.Fatalf("an unsynced cache must not serve reads: %v", names(got))
	}

	stop := make(chan struct{})
	defer close(stop)
	f.Start(stop)
	f.WaitForCacheSync(stop)
	if !k.HasSynced() {
		t.Fatal("cache did not sync")
	}
	got, err = c.Workloads(ctx)
	if err != nil || len(got) != 3 || got[0].Name != "c" || got[1].Name != "a" || got[2].Name != "b" {
		t.Fatalf("synced cache: %v %v", names(got), err)
	}
	pods, err := c.PodSummaries(ctx, "apps", "app=web")
	if err != nil || len(pods) != 2 || pods[0].Name != "web-0" || pods[1].Name != "web-1" {
		t.Fatalf("selector from cache: %+v %v", pods, err)
	}
	counts, err := c.PodCount(ctx)
	if err != nil || counts["n1"] != 1 || counts["n2"] != 1 {
		t.Fatalf("running pods per node: %v %v", counts, err)
	}
	r, err := c.NamespaceReadiness(ctx, "kube-system")
	if err != nil || r.Total != 1 || r.Ready != 0 {
		t.Fatalf("readiness: %+v %v", r, err)
	}

	c.dropCache(k)
	if got, _ := c.Workloads(ctx); len(got) != 1 || got[0].Name != "live" {
		t.Errorf("a dropped cache must fall back to live reads: %v", names(got))
	}
}

func TestEveryScopeInformerFeedsTheCache(t *testing.T) {
	f := informers.NewSharedInformerFactory(fake.NewClientset(), 0)
	k := NewCache(f)
	if len(k.synced) != len(informerScopes) {
		t.Fatalf("%d synced checks for %d scoped informers", len(k.synced), len(informerScopes))
	}
	scopes := map[string]bool{}
	for _, is := range informerScopes {
		scopes[is.scope] = true
	}
	for _, s := range []string{ScopeWorkloads, ScopeNetwork, ScopeStorage, ScopeNodes} {
		if !scopes[s] {
			t.Errorf("no informer refreshes %s", s)
		}
	}
}

func readyNode(name, bootID string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			NodeInfo:   corev1.NodeSystemInfo{BootID: bootID},
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
}

func TestWaitRebootedNeedsANewBootID(t *testing.T) {
	ctx := context.Background()
	live := fake.NewClientset(readyNode("cp-01", "boot-1"))
	c := &Client{Interface: live}
	before, err := c.NodeBootIDs(ctx, "cp-01")
	if err != nil || before["cp-01"] != "boot-1" {
		t.Fatalf("boot ids: %v %v", before, err)
	}
	if err := c.WaitRebooted(ctx, before, 0); err == nil {
		t.Fatal("a Ready node still on its old boot must not count as back")
	}
	if _, err := live.CoreV1().Nodes().Update(ctx, readyNode("cp-01", "boot-2"), metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := c.WaitRebooted(ctx, before, 0); err != nil {
		t.Fatalf("new boot and Ready: %v", err)
	}
	if err := c.WaitRebooted(ctx, map[string]string{"cp-01": ""}, 0); err != nil {
		t.Errorf("an unknown previous boot falls back to Ready: %v", err)
	}
}

func TestNodeReadsComeFromTheCacheOnceSynced(t *testing.T) {
	ctx := context.Background()
	c := &Client{Interface: fake.NewClientset(), rest: &rest.Config{Host: "http://127.0.0.1:1"}}
	f := informers.NewSharedInformerFactory(fake.NewClientset(readyNode("w-01", "b")), 0)
	k := NewCache(f)
	c.UseCache(k)
	stop := make(chan struct{})
	defer close(stop)
	f.Start(stop)
	f.WaitForCacheSync(stop)
	if err := c.WaitReady(ctx, []string{"w-01"}, 0, nil); err != nil {
		t.Fatalf("WaitReady from the cache: %v", err)
	}
	d, err := c.NodeDetail(ctx, "w-01")
	if err != nil || !d.Ready {
		t.Fatalf("NodeDetail from the cache: %+v %v", d, err)
	}
	if nodes, err := c.Nodes(ctx); err != nil || len(nodes) != 0 {
		t.Errorf("Nodes stays live: %v %v", nodes, err)
	}
}
