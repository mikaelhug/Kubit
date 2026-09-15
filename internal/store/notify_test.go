package store_test

import (
	"bytes"
	"context"
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
	_ = s.SetMachineWOL(ctx, "aa:aa:aa:aa:aa:01", true)
	id, _ := s.AddSnapshot(ctx, store.Snapshot{Cluster: "c", Path: "/x", Status: "ok"})
	_ = s.SetSnapshotStatus(ctx, id, "corrupt")
	_ = s.DeleteSnapshot(ctx, id)
	eid, _ := s.AddEvent(ctx, store.EventRow{Cluster: "c", Node: "n", Kind: "node.notready", Severity: "warn"})
	_ = s.ResolveEvents(ctx, "c", "n", "node.notready")
	_ = s.AckEvent(ctx, eid)
	_ = s.Audit(ctx, "c", "x", "")
	_ = s.SetValue(ctx, "k", "v")
	_ = s.DeleteCluster(ctx, "c")
	want := []string{"clusters/put", "clusters/put", "machines/put", "machines/put", "snapshots/put", "snapshots/put", "snapshots/delete", "events/resolve", "events/ack", "audit/put", "settings/put", "machines/put", "clusters/delete"}
	if len(got) != len(want) {
		t.Fatalf("got %d changes, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Table+"/"+got[i].Op != w {
			t.Errorf("change %d = %s/%s, want %s", i, got[i].Table, got[i].Op, w)
		}
	}
	if got[6].Cluster != "c" || got[7].Node != "n" || got[0].Key != "c" {
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
	b, err := store.Open(dir, c) // stands in for the CLI: a second connection pool to the same file
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
	if err := b.SetValue(ctx, "from", "cli"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-seen:
	case <-time.After(2 * time.Second):
		t.Fatal("external write not detected")
	}
	// A local write must not be reported as external.
	_ = a.SetValue(ctx, "from", "daemon")
	select {
	case ch := <-seen:
		t.Fatalf("local write reported as external: %+v", ch)
	case <-time.After(200 * time.Millisecond):
	}
}
