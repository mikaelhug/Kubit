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
	if _, split, _ := s.NodeMachineConfigSplit(ctx, "10.0.0.7"); split {
		t.Error("a node wiped outside Kubit no longer has its system volume")
	}
}

func TestUnassignMachineFollowsTheMACNotTheOldIP(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if err := s.PutCluster(ctx, store.ClusterRow{Name: "lab", Spec: []byte("spec: 1")}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertNode(ctx, store.NodeRow{IP: "10.0.0.9", MAC: "aa:00:00:00:00:09", Cluster: "lab", Hostname: "w-1", Source: "manual", State: "configured"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertNode(ctx, store.NodeRow{IP: "10.0.0.8", MAC: "aa:00:00:00:00:08", Cluster: "lab", Hostname: "w-2", Source: "manual", State: "configured"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UnassignMachine(ctx, "AA:00:00:00:00:09", "10.0.0.8", "configured"); err != nil {
		t.Fatal(err)
	}
	moved, _ := s.GetMachine(ctx, "aa:00:00:00:00:09")
	other, _ := s.GetMachine(ctx, "aa:00:00:00:00:08")
	if moved.Cluster != "" || other.Cluster != "lab" {
		t.Errorf("unassigned by MAC: moved=%q, holder of the old IP=%q", moved.Cluster, other.Cluster)
	}
}
