package store_test

import (
	"context"
	"testing"

	"github.com/mikael/kubit/internal/store"
)

func TestSettingsCacheInvalidatesAndCopies(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	v, err := s.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	v.DiscoverySubnets = append(v.DiscoverySubnets, "10.0.0.0/24")
	v.Alerts.IgnoreNamespaces = []string{"kube-system"}
	v.Alerts.SMTP.Password = "hunter2"
	if err := s.PutSettings(ctx, v); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetSettings(ctx)
	if len(got.DiscoverySubnets) != 1 || got.Alerts.SMTP.Password != "hunter2" {
		t.Fatalf("a write must invalidate the cache: %+v", got)
	}
	got.DiscoverySubnets[0] = "mutated"
	got.Alerts.IgnoreNamespaces[0] = "mutated"
	again, _ := s.GetSettings(ctx)
	if again.DiscoverySubnets[0] != "10.0.0.0/24" || again.Alerts.IgnoreNamespaces[0] != "kube-system" {
		t.Fatalf("callers must not reach the cached value: %+v", again)
	}
	if again.Alerts.SMTP.To == nil {
		t.Error("empty lists must stay empty, not null")
	}
	again.FactoryURL = "https://factory.example"
	if err := s.PutSettings(ctx, again); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.GetSettings(ctx); v.FactoryURL != "https://factory.example" {
		t.Errorf("second write not seen: %s", v.FactoryURL)
	}
}

func TestOpenWorkloadEvents(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	for _, e := range []store.EventRow{
		{Cluster: "c", Node: "default/web", Severity: "warn", Kind: "workload.degraded", Message: "m"},
		{Cluster: "c", Node: "cp-1", Severity: "warn", Kind: "node.notready", Message: "m"},
		{Cluster: "d", Node: "default/web", Severity: "warn", Kind: "workload.degraded", Message: "m"},
	} {
		if _, err := s.AddEvent(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.OpenWorkloadEvents(ctx, "c", 500)
	if err != nil || len(got) != 1 || got[0].Node != "default/web" {
		t.Fatalf("got %+v %v", got, err)
	}
	_ = s.AckClusterEvents(ctx, "c")
	if got, _ := s.OpenWorkloadEvents(ctx, "c", 500); len(got) != 0 {
		t.Errorf("acked events must drop out: %+v", got)
	}
}
