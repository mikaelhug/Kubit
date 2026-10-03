package k8s_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mikael/kubit/internal/k8s"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestPoolUsageFor(t *testing.T) {
	svcs := []k8s.Service{
		{Namespace: "traefik", Name: "traefik", Type: "LoadBalancer", ExternalIPs: []string{"10.0.0.200"}},
		{Namespace: "x", Name: "clusterip", Type: "ClusterIP", ExternalIPs: []string{"10.0.0.201"}},
		{Namespace: "y", Name: "outside", Type: "LoadBalancer", ExternalIPs: []string{"10.0.0.250"}},
	}
	u, err := k8s.PoolUsageFor("10.0.0.200-10.0.0.209", svcs)
	if err != nil {
		t.Fatal(err)
	}
	if u.Total != 10 || len(u.Allocated) != 1 || u.Allocated[0].IP != "10.0.0.200" || u.Allocated[0].Service != "traefik/traefik" {
		t.Errorf("%+v", u)
	}
	if _, err := k8s.PoolUsageFor("10.0.0.200", svcs); err == nil {
		t.Error("bad range must error")
	}
}

func TestSummariesMarshalEmptyListsAndCreationTime(t *testing.T) {
	created := metav1.NewTime(time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))
	c := &k8s.Client{Interface: fake.NewClientset(&corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: "a", Name: "headless", CreationTimestamp: created}})}
	svcs, err := c.Services(t.Context())
	if err != nil || len(svcs) != 1 {
		t.Fatalf("%v %v", svcs, err)
	}
	b, _ := json.Marshal(svcs[0])
	if !strings.Contains(string(b), `"ports":[]`) || !strings.Contains(string(b), `"createdAt":"2026-09-01T12:00:00Z"`) {
		t.Errorf("service: %s", b)
	}
	ws, err := c.Workloads(t.Context())
	if b, _ := json.Marshal(ws); err != nil || string(b) != "[]" {
		t.Errorf("no workloads: %s %v", b, err)
	}
}
