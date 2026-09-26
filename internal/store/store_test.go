package store_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/mikael/kubit/internal/store"
)

func open(t *testing.T) *store.Store {
	t.Helper()
	c, err := store.NewCrypto(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(t.TempDir(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCryptoRoundTripAndTamper(t *testing.T) {
	c, _ := store.NewCrypto(bytes.Repeat([]byte{1}, 32))
	sealed, err := c.Seal([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := c.Open(sealed)
	if err != nil || string(plain) != "secret" {
		t.Fatalf("open: %q %v", plain, err)
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := c.Open(sealed); err == nil {
		t.Error("tampered ciphertext must fail")
	}
	if _, err := store.NewCrypto([]byte("short")); err == nil {
		t.Error("key length must be enforced")
	}
}

func TestClusterAndSecrets(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if err := s.PutCluster(ctx, store.ClusterRow{Name: "dev", Spec: []byte("spec: 1"), SchematicID: "abc"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetClusterSecrets(ctx, "dev"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("no secrets yet: %v", err)
	}
	if err := s.PutClusterSecrets(ctx, "dev", store.ClusterSecrets{SecretsBundle: []byte("bundle"), Talosconfig: []byte("tc")}); err != nil {
		t.Fatal(err)
	}
	sec, err := s.GetClusterSecrets(ctx, "dev")
	if err != nil || string(sec.SecretsBundle) != "bundle" || string(sec.Talosconfig) != "tc" || sec.Kubeconfig != nil {
		t.Fatalf("secrets: %+v %v", sec, err)
	}
	if err := s.SetKubeconfig(ctx, "dev", []byte("kc")); err != nil {
		t.Fatal(err)
	}
	if sec, _ = s.GetClusterSecrets(ctx, "dev"); string(sec.Kubeconfig) != "kc" {
		t.Errorf("kubeconfig = %q", sec.Kubeconfig)
	}
	if err := s.PutCluster(ctx, store.ClusterRow{Name: "dev", Spec: []byte("spec: 2"), State: "ready"}); err != nil {
		t.Fatal(err)
	}
	c, err := s.GetCluster(ctx, "dev")
	if err != nil || string(c.Spec) != "spec: 2" || c.State != "ready" {
		t.Fatalf("upsert: %+v %v", c, err)
	}
	if err := s.DeleteCluster(ctx, "dev"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetClusterSecrets(ctx, "dev"); !errors.Is(err, store.ErrNotFound) {
		t.Error("secrets must cascade on cluster delete")
	}
}

func TestNodes(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if err := s.UpsertNode(ctx, store.NodeRow{IP: "10.0.0.1", MAC: "aa:bb", Arch: "arm64", Source: "scan", State: "maintenance", Hardware: []byte(`{"cpus":2}`)}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutCluster(ctx, store.ClusterRow{Name: "dev", Spec: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	if err := s.AssignNode(ctx, "10.0.0.1", "dev", "cp-01", "controlplane"); err != nil {
		t.Fatal(err)
	}
	// A rescan reports no membership and no hardware; both must survive.
	if err := s.UpsertNode(ctx, store.NodeRow{IP: "10.0.0.1", Source: "scan", State: "configured"}); err != nil {
		t.Fatal(err)
	}
	n, err := s.GetNode(ctx, "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if n.Cluster != "dev" || n.Hostname != "cp-01" || n.Role != "controlplane" || n.MAC != "aa:bb" || string(n.Hardware) != `{"cpus":2}` || n.State != "configured" {
		t.Errorf("rescan clobbered fields: %+v", n)
	}
	if err := s.PutNodeMachineConfig(ctx, "10.0.0.1", []byte("cfg"), true); err != nil {
		t.Fatal(err)
	}
	if cfg, err := s.GetNodeMachineConfig(ctx, "10.0.0.1"); err != nil || string(cfg) != "cfg" {
		t.Errorf("machine config: %q %v", cfg, err)
	}
	if err := s.PutNodeMachineConfig(ctx, "10.0.0.1", []byte("cfg2"), false); err != nil {
		t.Fatal(err)
	}
	if !s.NodeSystemSplit(ctx, "10.0.0.1") {
		t.Error("a node once installed with a system volume keeps the mark")
	}
	if _, err := s.GetNodeMachineConfig(ctx, "10.0.0.2"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing config: %v", err)
	}
	list, err := s.ListNodes(ctx, "dev")
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %v", list, err)
	}
	if err := s.DeleteCluster(ctx, "dev"); err != nil {
		t.Fatal(err)
	}
	if n, _ = s.GetNode(ctx, "10.0.0.1"); n.Cluster != "" {
		t.Error("deleting a cluster must unassign, not delete, its nodes")
	}
}

func TestReopenKeepsData(t *testing.T) {
	c, _ := store.NewCrypto(bytes.Repeat([]byte{7}, 32))
	dir := t.TempDir()
	s, err := store.Open(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PutCluster(context.Background(), store.ClusterRow{Name: "a", Spec: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = store.Open(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if rows, _ := s.ListClusters(context.Background()); len(rows) != 1 {
		t.Errorf("after reopen: %v", rows)
	}
}

func TestMachineIdentityFollowsMAC(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	// First lease.
	if err := s.UpsertNode(ctx, store.NodeRow{IP: "10.0.0.5", MAC: "AA:BB:CC:DD:EE:01", Source: "scan", State: "maintenance"}); err != nil {
		t.Fatal(err)
	}
	// Same machine, new lease; another machine takes the old address.
	if err := s.UpsertNode(ctx, store.NodeRow{IP: "10.0.0.9", MAC: "aa:bb:cc:dd:ee:01", Source: "scan", State: "maintenance"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertNode(ctx, store.NodeRow{IP: "10.0.0.5", MAC: "aa:bb:cc:dd:ee:02", Source: "scan", State: "maintenance"}); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListNodes(ctx, "")
	if len(list) != 2 {
		t.Fatalf("expected 2 machines, got %d: %+v", len(list), list)
	}
	m1, err := s.GetMachine(ctx, "aa:bb:cc:dd:ee:01")
	if err != nil || m1.IP != "10.0.0.9" || len(m1.IPsSeen) != 1 || m1.IPsSeen[0] != "10.0.0.5" {
		t.Errorf("machine 1 should have moved to .9 remembering .5: %+v %v", m1, err)
	}
	m2, _ := s.GetNode(ctx, "10.0.0.5")
	if m2.MAC != "aa:bb:cc:dd:ee:02" {
		t.Errorf(".5 must now belong to machine 2, got %s", m2.MAC)
	}
}

func TestUpdateLabHostMergesConcurrentWriters(t *testing.T) {
	s := open(t)
	defer s.Close()
	ctx := context.Background()
	mac := "52:54:00:6b:01:01"
	if err := s.UpsertNode(ctx, store.NodeRow{IP: "10.0.0.2", MAC: mac, State: "labhost", Source: "labhost"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLabHost(ctx, mac, &store.LabHost{State: "ready"}); err != nil {
		t.Fatal(err)
	}
	// One writer touches State, another touches Failures, concurrently: with a blind
	// full-blob write one would clobber the other; the per-host merge keeps both.
	done := make(chan struct{}, 2)
	go func() {
		_ = s.UpdateLabHost(ctx, mac, func(l *store.LabHost) { l.State = "updating" })
		done <- struct{}{}
	}()
	go func() { _ = s.UpdateLabHost(ctx, mac, func(l *store.LabHost) { l.Failures = 7 }); done <- struct{}{} }()
	<-done
	<-done
	m, err := s.GetMachine(ctx, mac)
	if err != nil {
		t.Fatal(err)
	}
	if m.LabHost.State != "updating" || m.LabHost.Failures != 7 {
		t.Fatalf("merge lost a field: state=%q failures=%d", m.LabHost.State, m.LabHost.Failures)
	}
	// A machine with no lab-host record is a no-op, not a panic.
	_ = s.UpsertNode(ctx, store.NodeRow{IP: "10.0.0.3", MAC: "52:54:00:6b:01:02", State: "maintenance"})
	if err := s.UpdateLabHost(ctx, "52:54:00:6b:01:02", func(l *store.LabHost) { l.State = "x" }); err != nil {
		t.Fatalf("update on a non-lab-host must be a no-op: %v", err)
	}
}
