package cluster

import (
	"bytes"
	"errors"
	"testing"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/store"
)

func node(host, mac, ip string) config.Node {
	return config.Node{Hostname: host, MAC: mac, IP: ip, Role: config.RoleControlPlane}
}

func cl(nodes ...config.Node) *config.Cluster {
	c := &config.Cluster{}
	c.Spec.Nodes = nodes
	return c
}

func TestSameNodesIgnoresIP(t *testing.T) {
	if !sameNodes(cl(node("cp-01", "52:54:00:00:00:01", "10.0.0.5")), cl(node("cp-01", "52:54:00:00:00:01", "10.0.0.9"))) {
		t.Error("a changed IP on the same MAC must still be the same node set")
	}
	if sameNodes(cl(node("cp-01", "52:54:00:00:00:01", "10.0.0.5")), cl(node("cp-01", "52:54:00:00:00:02", "10.0.0.5"))) {
		t.Error("a different MAC must be a different node set")
	}
	if sameNodes(cl(node("a", "52:54:00:00:00:01", "10.0.0.5")), cl(node("a", "52:54:00:00:00:01", "10.0.0.5"), node("b", "52:54:00:00:00:02", "10.0.0.6"))) {
		t.Error("different node counts are different sets")
	}
}

func TestSaveExistingNeverRecreatesAForgottenCluster(t *testing.T) {
	crypto, _ := store.NewCrypto(bytes.Repeat([]byte{5}, 32))
	st, err := store.Open(t.TempDir(), crypto)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := t.Context()
	m := NewManager(st, t.TempDir())
	c, err := config.Parse([]byte("apiVersion: kubit.dev/v1\nkind: Cluster\nmetadata: {name: c}\nspec:\n  nodes:\n    - {hostname: a, ip: 10.0.0.1, role: controlplane, installDisk: {path: /dev/sda}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SaveCluster(ctx, c, StateReady); err != nil {
		t.Fatal(err)
	}
	c.Spec.TalosVersion = "v9.9.9"
	if err := m.saveExisting(ctx, c); err != nil {
		t.Fatalf("an existing cluster saves: %v", err)
	}
	if err := st.DeleteCluster(ctx, "c"); err != nil {
		t.Fatal(err)
	}
	if err := m.saveExisting(ctx, c); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a forgotten cluster must not be saved: %v", err)
	}
	if _, err := st.GetCluster(ctx, "c"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the forgotten cluster came back: %v", err)
	}
}
