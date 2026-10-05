package store_test

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/mikael/kubit/internal/store"
)

func TestRemovedFeaturesLeaveNoSchema(t *testing.T) {
	ctx := context.Background()
	c, _ := store.NewCrypto(bytes.Repeat([]byte{7}, 32))
	dir := t.TempDir()
	s, err := store.Open(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertNode(ctx, store.NodeRow{MAC: "aa:aa:aa:aa:aa:01", IP: "10.0.0.1", Source: "scan", State: "maintenance"}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	db, err := sql.Open("sqlite", filepath.Join(dir, "kubit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range []string{"users", "sessions", "operations", "operation_log", "audit_log"} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&n); err != nil || n != 0 {
			t.Errorf("table %s still exists (%v)", table, err)
		}
	}
	for _, col := range []string{"wol", "oob", "provision", "labhost", "host", "provision_kind"} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('machines') WHERE name = ?`, col).Scan(&n); err != nil || n != 0 {
			t.Errorf("machines.%s still exists (%v)", col, err)
		}
	}
}
