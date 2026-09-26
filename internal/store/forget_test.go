package store_test

import (
	"context"
	"testing"

	"github.com/mikael/kubit/internal/store"
)

func TestDeleteClusterResolvesItsAlerts(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if err := s.PutCluster(ctx, store.ClusterRow{Name: "lab", Spec: []byte("spec: 1")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddEvent(ctx, store.EventRow{Cluster: "lab", Node: "Deployment/kubit-builds/registry", Severity: "warn", Kind: "workload.unavailable", Message: "0/1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteCluster(ctx, "lab"); err != nil {
		t.Fatal(err)
	}
	if n := s.OpenEventCount(ctx, "lab"); n != 0 {
		t.Errorf("a new cluster with the same name would inherit %d open alerts", n)
	}
	if evs, _ := s.Events(ctx, "lab", 10, false); len(evs) != 1 {
		t.Errorf("history must stay: %d events", len(evs))
	}
}

func TestWipedMemberLosesItsSystemVolumeMark(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if err := s.PutCluster(ctx, store.ClusterRow{Name: "lab", Spec: []byte("spec: 1")}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertNode(ctx, store.NodeRow{IP: "10.0.0.7", MAC: "aa:00:00:00:00:07", Cluster: "lab", Hostname: "w-1", Source: "manual", State: "configured"}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutNodeMachineConfig(ctx, "10.0.0.7", []byte("cfg"), true); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertNode(ctx, store.NodeRow{IP: "10.0.0.7", MAC: "aa:00:00:00:00:07", Source: "scan", State: "maintenance"}); err != nil {
		t.Fatal(err)
	}
	if s.NodeSystemSplit(ctx, "10.0.0.7") {
		t.Error("a node wiped outside Kubit no longer has its system volume")
	}
}
