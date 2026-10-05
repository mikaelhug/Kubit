package store_test

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/mikael/kubit/internal/store"
)

func TestNotifierPerTable(t *testing.T) {
	c, _ := store.NewCrypto(bytes.Repeat([]byte{5}, 32))
	s, err := store.Open(t.TempDir(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var got []store.Change
	s.OnChange(func(ch store.Change) { got = append(got, ch) })
	ctx := t.Context()
	_ = s.PutCluster(ctx, store.ClusterRow{Name: "c", Spec: []byte("x"), State: "provisioning"})
	_ = s.SetClusterState(ctx, "c", "ready")
	_ = s.UpsertNode(ctx, store.NodeRow{IP: "10.0.0.1", MAC: "aa:aa:aa:aa:aa:01", State: "maintenance"})
	_ = s.UpsertNode(ctx, store.NodeRow{IP: "10.0.0.1", MAC: "aa:aa:aa:aa:aa:01", State: "configured"})
	id, _ := s.AddSnapshot(ctx, store.Snapshot{Cluster: "c", Path: "/x", Status: "ok"})
	_ = s.SetSnapshotStatus(ctx, id, "corrupt")
	_ = s.DeleteSnapshot(ctx, id)
	eid, _ := s.AddEvent(ctx, store.EventRow{Cluster: "c", Node: "n", Kind: "node.notready", Severity: "warn"})
	_ = s.ResolveEvents(ctx, "c", "n", "node.notready")
	_ = s.AckEvent(ctx, eid)
	_ = s.PutSettings(ctx, store.DefaultSettings())
	_ = s.DeleteCluster(ctx, "c")
	want := []string{"clusters/put", "clusters/put", "machines/put", "machines/put", "snapshots/put", "snapshots/put", "snapshots/delete", "events/resolve", "events/ack", "settings/put", "machines/put", "clusters/delete"}
	if len(got) != len(want) {
		t.Fatalf("got %d changes, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Table+"/"+got[i].Op != w {
			t.Errorf("change %d = %s/%s, want %s", i, got[i].Table, got[i].Op, w)
		}
	}
	if got[6].Cluster != "c" || got[7].Node != "n" || got[0].Key != "c" || got[8].Cluster != "c" {
		t.Errorf("keys/cluster/node not carried: %+v", got)
	}
}

func TestWatchExternalSeesOtherProcessWrites(t *testing.T) {
	c, _ := store.NewCrypto(bytes.Repeat([]byte{6}, 32))
	dir := t.TempDir()
	a, err := store.Open(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := store.Open(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	seen := make(chan store.Change, 4)
	a.OnChange(func(ch store.Change) {
		if ch.Table == "*" {
			seen <- ch
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go a.WatchExternal(ctx, 50*time.Millisecond)
	time.Sleep(120 * time.Millisecond)
	if err := b.PutSettings(ctx, store.DefaultSettings()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-seen:
	case <-time.After(2 * time.Second):
		t.Fatal("external write not detected")
	}
	_ = a.PutSettings(ctx, store.DefaultSettings())
	select {
	case ch := <-seen:
		t.Fatalf("local write reported as external: %+v", ch)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestOnlyOtherConnectionsCountAsExternal(t *testing.T) {
	c, _ := store.NewCrypto(bytes.Repeat([]byte{8}, 32))
	dir := t.TempDir()
	s, err := store.Open(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	external := make(chan store.Change, 16)
	s.OnChange(func(ch store.Change) {
		if ch.Table == "*" {
			external <- ch
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go s.WatchExternal(ctx, 10*time.Millisecond)
	time.Sleep(30 * time.Millisecond)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.PutCluster(ctx, store.ClusterRow{Name: "c", Spec: []byte("x"), State: "ready"}))
	must(s.PutClusterSecrets(ctx, "c", store.ClusterSecrets{SecretsBundle: []byte("b"), Talosconfig: []byte("t")}))
	must(s.UpsertNode(ctx, store.NodeRow{MAC: "aa:aa:aa:aa:aa:09", IP: "10.0.0.9", State: "maintenance"}))
	for _, write := range []func() error{
		func() error { return s.AddSamples(ctx, "c", time.Now(), []store.Sample{{CPUMilli: 1}}) },
		func() error {
			_, err := s.AddEvent(ctx, store.EventRow{Cluster: "c", Severity: "info", Kind: "k", Message: "m"})
			return err
		},
		func() error { return s.PutNodeMachineConfig(ctx, "10.0.0.9", []byte("cfg"), false) },
		func() error { return s.Prune(ctx) },
		func() error { return s.Checkpoint(ctx) },
	} {
		must(write())
		time.Sleep(30 * time.Millisecond)
	}
	select {
	case ch := <-external:
		t.Fatalf("a local write was reported as external: %+v", ch)
	case <-time.After(100 * time.Millisecond):
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "kubit.db"))
	must(err)
	defer db.Close()
	_, err = db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('from', 'elsewhere')`)
	must(err)
	select {
	case <-external:
	case <-time.After(2 * time.Second):
		t.Fatal("a write from another connection was not detected")
	}
}

func TestExternalSettingsWriteBesideALocalWriteRefreshesSettings(t *testing.T) {
	c, _ := store.NewCrypto(bytes.Repeat([]byte{7}, 32))
	dir := t.TempDir()
	a, err := store.Open(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := store.Open(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if _, err := a.GetSettings(ctx); err != nil {
		t.Fatal(err)
	}
	go a.WatchExternal(ctx, 300*time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	set, _ := b.GetSettings(ctx)
	set.FactoryURL = "https://factory.example"
	if err := b.PutSettings(ctx, set); err != nil {
		t.Fatal(err)
	}
	if err := a.PutCluster(ctx, store.ClusterRow{Name: "c", Spec: []byte("x"), State: "ready"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got, _ := a.GetSettings(ctx); got.FactoryURL == "https://factory.example" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("cached settings survived an external write")
}
