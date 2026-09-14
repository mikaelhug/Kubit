package k8s_test

import (
	"testing"

	"github.com/mikael/kubit/internal/k8s"
)

func TestPoolUsageFor(t *testing.T) {
	svcs := []k8s.Service{
		{Namespace: "ingress-nginx", Name: "ingress-nginx-controller", Type: "LoadBalancer", ExternalIPs: []string{"10.0.0.200"}},
		{Namespace: "x", Name: "clusterip", Type: "ClusterIP", ExternalIPs: []string{"10.0.0.201"}},
		{Namespace: "y", Name: "outside", Type: "LoadBalancer", ExternalIPs: []string{"10.0.0.250"}},
	}
	u, err := k8s.PoolUsageFor("10.0.0.200-10.0.0.209", svcs)
	if err != nil {
		t.Fatal(err)
	}
	if u.Total != 10 || len(u.Allocated) != 1 || u.Allocated[0].IP != "10.0.0.200" || u.Allocated[0].Service != "ingress-nginx/ingress-nginx-controller" {
		t.Errorf("%+v", u)
	}
	if _, err := k8s.PoolUsageFor("10.0.0.200", svcs); err == nil {
		t.Error("bad range must error")
	}
}
