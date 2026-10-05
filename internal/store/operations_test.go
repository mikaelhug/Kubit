package store_test

import (
	"bytes"
	"testing"

	"github.com/mikael/kubit/internal/store"
)

func TestPutClusterWithoutStateKeepsStoredState(t *testing.T) {
	c, _ := store.NewCrypto(bytes.Repeat([]byte{3}, 32))
	s, err := store.Open(t.TempDir(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := t.Context()
	if err := s.PutCluster(ctx, store.ClusterRow{Name: "c", Spec: []byte("x"), State: "provisioning"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetClusterState(ctx, "c", "ready"); err != nil {
		t.Fatal(err)
	}
	if err := s.PutCluster(ctx, store.ClusterRow{Name: "c", Spec: []byte("y")}); err != nil {
		t.Fatal(err)
	}
	row, err := s.GetCluster(ctx, "c")
	if err != nil || row.State != "ready" || string(row.Spec) != "y" {
		t.Fatalf("got %+v %v", row, err)
	}
}

func TestForgetReleasesMachinesAndWipedMemberDropsMembership(t *testing.T) {
	c, _ := store.NewCrypto(bytes.Repeat([]byte{4}, 32))
	s, err := store.Open(t.TempDir(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := t.Context()
	if err := s.PutCluster(ctx, store.ClusterRow{Name: "c", Spec: []byte("x"), State: "ready"}); err != nil {
		t.Fatal(err)
	}
	for i, mac := range []string{"aa:aa:aa:aa:aa:01", "aa:aa:aa:aa:aa:02"} {
		if err := s.UpsertNode(ctx, store.NodeRow{IP: "10.0.0.1" + string(rune('0'+i)), MAC: mac, Cluster: "c", Hostname: "n", Pool: "worker", Role: "worker", State: "ready"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UpsertNode(ctx, store.NodeRow{IP: "10.0.0.10", MAC: "aa:aa:aa:aa:aa:01", Source: "scan", State: "maintenance"}); err != nil {
		t.Fatal(err)
	}
	m, _ := s.GetMachine(ctx, "aa:aa:aa:aa:aa:01")
	if m.Cluster != "" || m.Hostname != "" || m.State != "maintenance" {
		t.Fatalf("wiped member should be released: %+v", m)
	}
	if err := s.UpsertNode(ctx, store.NodeRow{IP: "10.0.0.11", MAC: "aa:aa:aa:aa:aa:02", Source: "scan", State: "configured"}); err != nil {
		t.Fatal(err)
	}
	m, _ = s.GetMachine(ctx, "aa:aa:aa:aa:aa:02")
	if m.Cluster != "c" || m.Hostname != "n" {
		t.Fatalf("configured member should stay: %+v", m)
	}
	if err := s.DeleteCluster(ctx, "c"); err != nil {
		t.Fatal(err)
	}
	m, _ = s.GetMachine(ctx, "aa:aa:aa:aa:aa:02")
	if m.Cluster != "" || m.State != "configured" {
		t.Fatalf("forget should release the machine: %+v", m)
	}
}
