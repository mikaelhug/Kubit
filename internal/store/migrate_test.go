package store_test

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/mikael/kubit/internal/store"
)

func TestReleasedLabHostsMigrateToUnknown(t *testing.T) {
	ctx := context.Background()
	c, _ := store.NewCrypto(bytes.Repeat([]byte{7}, 32))
	dir := t.TempDir()
	s, err := store.Open(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.UpsertNode(ctx, store.NodeRow{MAC: "aa:aa:aa:aa:aa:01", IP: "10.0.0.1", Source: "labhost", State: "configured"})
	_ = s.UpsertNode(ctx, store.NodeRow{MAC: "aa:aa:aa:aa:aa:02", IP: "10.0.0.2", Source: "scan", State: "configured"})
	s.Close()
	db, err := sql.Open("sqlite", filepath.Join(dir, "kubit.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM schema_version WHERE version >= 12`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err = store.Open(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	released, _ := s.GetMachine(ctx, "aa:aa:aa:aa:aa:01")
	foreign, _ := s.GetMachine(ctx, "aa:aa:aa:aa:aa:02")
	if released.State != "unknown" || foreign.State != "configured" {
		t.Errorf("released %s, foreign %s", released.State, foreign.State)
	}
}
