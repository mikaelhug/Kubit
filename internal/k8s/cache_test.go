package k8s

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
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
