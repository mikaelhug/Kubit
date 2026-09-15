package store_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/mikael/kubit/internal/store"
)

func TestLastFinished(t *testing.T) {
	c, _ := store.NewCrypto(bytes.Repeat([]byte{3}, 32))
	s, err := store.Open(t.TempDir(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := t.Context()
	if !s.LastFinished(ctx, "c", []string{"etcd.restore"}).IsZero() {
		t.Fatal("no operations yet: expected zero time")
	}
	id, err := s.CreateOperation(ctx, "c", "etcd.restore", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !s.LastFinished(ctx, "c", []string{"etcd.restore"}).IsZero() {
		t.Fatal("running operation must not count")
	}
	if err := s.FinishOperation(ctx, id, "done"); err != nil {
		t.Fatal(err)
	}
	got := s.LastFinished(ctx, "c", []string{"node.reboot", "etcd.restore"})
	if time.Since(got) > time.Minute || time.Since(got) < 0 {
		t.Fatalf("finished_at parsed wrong: %v", got)
	}
	if !s.LastFinished(ctx, "other", []string{"etcd.restore"}).IsZero() {
		t.Fatal("other cluster must not see it")
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
	// A rescan finding a member in maintenance mode: it was wiped outside Kubit.
	if err := s.UpsertNode(ctx, store.NodeRow{IP: "10.0.0.10", MAC: "aa:aa:aa:aa:aa:01", Source: "scan", State: "maintenance"}); err != nil {
		t.Fatal(err)
	}
	m, _ := s.GetMachine(ctx, "aa:aa:aa:aa:aa:01")
	if m.Cluster != "" || m.Hostname != "" || m.State != "maintenance" {
		t.Fatalf("wiped member should be released: %+v", m)
	}
	// A rescan seeing a member as configured keeps it.
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
