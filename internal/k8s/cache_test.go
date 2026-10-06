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

	cached := fake.NewClientset(deployment("kube-system", "b"), deployment("kube", "a"), deployment("default", "c"))
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
	if err != nil || len(got) != 3 {
		t.Fatalf("synced cache: %v %v", names(got), err)
	}

	c.dropCache(k)
	if got, _ := c.Workloads(ctx); len(got) != 1 || got[0].Name != "live" {
		t.Errorf("a dropped cache must fall back to live reads: %v", names(got))
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
}
