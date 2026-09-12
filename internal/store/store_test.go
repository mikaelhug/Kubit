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
	if err := s.PutNodeMachineConfig(ctx, "10.0.0.1", []byte("cfg")); err != nil {
		t.Fatal(err)
	}
	if cfg, err := s.GetNodeMachineConfig(ctx, "10.0.0.1"); err != nil || string(cfg) != "cfg" {
		t.Errorf("machine config: %q %v", cfg, err)
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
