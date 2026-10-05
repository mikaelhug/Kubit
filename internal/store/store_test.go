package store_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strconv"
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
	if err := s.UpsertNode(ctx, store.NodeRow{IP: "10.0.0.1", MAC: "aa:bb", Cluster: "dev", Hostname: "cp-01", Role: "controlplane", Source: "scan", State: "maintenance"}); err != nil {
		t.Fatal(err)
	}
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
	if _, split, _ := s.NodeMachineConfigSplit(ctx, "10.0.0.1"); !split {
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
	if err := s.UpsertNode(ctx, store.NodeRow{IP: "10.0.0.5", MAC: "AA:BB:CC:DD:EE:01", Source: "scan", State: "maintenance"}); err != nil {
		t.Fatal(err)
	}
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

func TestNotFoundWrapsMissingRows(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	for name, err := range map[string]error{
		"cluster":  func() error { _, err := s.GetCluster(ctx, "nope"); return err }(),
		"secrets":  func() error { _, err := s.GetClusterSecrets(ctx, "nope"); return err }(),
		"machine":  func() error { _, err := s.GetMachine(ctx, "52:54:00:00:00:99"); return err }(),
		"node":     func() error { _, err := s.GetNode(ctx, "10.9.9.9"); return err }(),
		"config":   func() error { _, err := s.GetNodeMachineConfig(ctx, "10.9.9.9"); return err }(),
		"op":       func() error { _, err := s.GetOperation(ctx, 999); return err }(),
		"snapshot": func() error { _, err := s.GetSnapshot(ctx, 999); return err }(),
	} {
		if !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s: %v is not ErrNotFound", name, err)
		}
	}
	if _, err := s.GetCluster(ctx, "nope"); err == nil || err.Error() != `cluster "nope": not found` {
		t.Errorf("message: %v", err)
	}
}

func TestOperationRoundTrip(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	id, err := s.CreateOperation(ctx, "", "test.op", []byte(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = s.AppendOperationLog(ctx, id, "line")
	_ = s.SetOperationSteps(ctx, id, []byte(`[{"id":"x"}]`))
	_ = s.FinishOperation(ctx, id, "done")
	op, err := s.GetOperation(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if op.Kind != "test.op" || op.Status != "done" || op.Log != "line\n" || string(op.Request) != `{"a":1}` || string(op.Steps) != `[{"id":"x"}]` || op.FinishedAt == "" || op.Artifact != nil {
		t.Errorf("get: %+v", op)
	}
	list, err := s.ListOperations(ctx, 10)
	if err != nil || len(list) != 1 || list[0].ID != id || list[0].Log != "" || string(list[0].Steps) != `[{"id":"x"}]` {
		t.Errorf("list: %+v %v", list, err)
	}
}

func TestOpenAlertsIgnoreInfoAndLimits(t *testing.T) {
	s := open(t)
	ctx := t.Context()
	if _, err := s.AddEvent(ctx, store.EventRow{Cluster: "c", Node: "n", Severity: "critical", Kind: "talos.unreachable", Message: "down"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1100; i++ {
		if _, err := s.AddEvent(ctx, store.EventRow{Cluster: "c", Severity: "info", Kind: "node.cordoned", Message: "info"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AddEvent(ctx, store.EventRow{Cluster: "other", Severity: "warn", Kind: "node.notready", Message: "x"}); err != nil {
		t.Fatal(err)
	}
	alerts, err := s.OpenAlerts(ctx, "c")
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 1 || alerts[0].Kind != "talos.unreachable" || alerts[0].Node != "n" {
		t.Errorf("open alerts %+v", alerts)
	}
}

func TestGetAuditReadsTheRow(t *testing.T) {
	s := open(t)
	ctx := t.Context()
	var ids []string
	s.OnChange(func(c store.Change) {
		if c.Table == "audit" {
			ids = append(ids, c.Key)
		}
	})
	_ = s.Audit(ctx, "a", "first", "1")
	_ = s.Audit(ctx, "b", "second", "2")
	id, _ := strconv.ParseInt(ids[0], 10, 64)
	e, err := s.GetAudit(ctx, id)
	if err != nil || e.Action != "first" || e.Cluster != "a" {
		t.Errorf("audit %d: %+v %v", id, e, err)
	}
	if _, err := s.GetAudit(ctx, 999); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing entry: %v", err)
	}
}

func TestListsAreEmptyNotNil(t *testing.T) {
	s := open(t)
	ctx := t.Context()
	clusters, _ := s.ListClusters(ctx)
	nodes, _ := s.ListNodes(ctx, "")
	ops, _ := s.ListOperations(ctx, 10)
	if clusters == nil || nodes == nil || ops == nil {
		t.Errorf("empty lists must be [] not null: %v %v %v", clusters == nil, nodes == nil, ops == nil)
	}
}

func TestMachineChangesAreKeyedByMAC(t *testing.T) {
	s := open(t)
	ctx := t.Context()
	if err := s.PutCluster(ctx, store.ClusterRow{Name: "c", Spec: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertNode(ctx, store.NodeRow{MAC: "AA:BB:CC:00:00:01", IP: "10.0.0.5", State: "maintenance"}); err != nil {
		t.Fatal(err)
	}
	var keys []string
	s.OnChange(func(c store.Change) {
		if c.Table == "machines" {
			keys = append(keys, c.Key)
		}
	})
	_ = s.SetNodeState(ctx, "10.0.0.5", "configured")
	_ = s.UpsertNode(ctx, store.NodeRow{IP: "10.0.0.5", Cluster: "c", Hostname: "n1", Role: "worker", State: "configured"})
	_ = s.UnassignNode(ctx, "10.0.0.5", "maintenance")
	_ = s.SetNodeState(ctx, "10.9.9.9", "configured")
	if !slices.Equal(keys, []string{"aa:bb:cc:00:00:01", "aa:bb:cc:00:00:01", "aa:bb:cc:00:00:01"}) {
		t.Errorf("change keys %v", keys)
	}
	if m, _ := s.GetMachine(ctx, "aa:bb:cc:00:00:01"); m.State != "maintenance" || m.Hostname != "" {
		t.Errorf("machine %+v", m)
	}
}

func TestUpsertNodeSkipsARepeatedSighting(t *testing.T) {
	s := open(t)
	ctx := t.Context()
	var n int
	s.OnChange(func(c store.Change) {
		if c.Table == "machines" {
			n++
		}
	})
	row := store.NodeRow{MAC: "aa:bb:cc:00:00:02", IP: "10.0.0.6", Hostname: "h", State: "configured", Hardware: []byte(`{"cpus":4}`)}
	for i := 0; i < 3; i++ {
		if err := s.UpsertNode(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	if n != 1 {
		t.Errorf("repeated sightings notified %d times, want 1", n)
	}
	row.TalosVersion = "v1.11.0"
	if err := s.UpsertNode(ctx, row); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.GetMachine(ctx, row.MAC); n != 2 || m.TalosVersion != "v1.11.0" {
		t.Errorf("a changed sighting must write and notify: n=%d %+v", n, m)
	}
	if err := s.UpsertNode(ctx, store.NodeRow{MAC: "aa:bb:cc:00:00:03", IP: "10.0.0.6", State: "maintenance"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertNode(ctx, row); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.GetMachine(ctx, row.MAC); m.IP != "10.0.0.6" || n != 4 {
		t.Errorf("an address taken back must write: n=%d ip=%s", n, m.IP)
	}
}
