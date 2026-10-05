package store

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestHotQueriesUseTheirIndexes(t *testing.T) {
	s := openStore(t)
	ctx := t.Context()
	for _, c := range []struct {
		query string
		args  []any
		index string
	}{
		{`SELECT COUNT(*) FROM events WHERE cluster = ? AND node = ? AND kind = ? AND acked = 0`, []any{"c", "n", "k"}, "events_open"},
		{`SELECT COUNT(*) FROM events WHERE cluster = ? AND acked = 0 AND severity IN ('warn', 'critical')`, []any{"c"}, "events_open"},
		{`UPDATE events SET acked = 1 WHERE cluster = ? AND node = ? AND kind = ? AND acked = 0`, []any{"c", "n", "k"}, "events_open"},
	} {
		rows, err := s.db.QueryContext(ctx, `EXPLAIN QUERY PLAN `+c.query, c.args...)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		if !strings.Contains(strings.Join(plan, "\n"), c.index) {
			t.Errorf("%s\nplan %v, want %s", c.query, plan, c.index)
		}
	}
}

func TestStoreWritesWithNormalSync(t *testing.T) {
	s := openStore(t)
	var mode int
	if err := s.db.QueryRowContext(t.Context(), `PRAGMA synchronous`).Scan(&mode); err != nil || mode != 1 {
		t.Errorf("synchronous = %d (%v), want 1 (NORMAL)", mode, err)
	}
}

func TestPruneKeepsRecentHistory(t *testing.T) {
	s := openStore(t)
	ctx := t.Context()
	old := time.Now().Add(-100 * 24 * time.Hour).UTC().Format(time.RFC3339)
	recent := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO events (ts, cluster, severity, kind, message, acked) VALUES (?, 'c', 'warn', 'old.acked', '', 1), (?, 'c', 'warn', 'old.open', '', 0), (?, 'c', 'warn', 'new.acked', '', 1)`, old, old, recent)
	if err := s.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	count := func(q string) int {
		t.Helper()
		var n int
		if err := s.db.QueryRowContext(ctx, q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if got := count(`SELECT COUNT(*) FROM events WHERE kind = 'old.acked'`); got != 0 {
		t.Errorf("old acked events kept: %d", got)
	}
	if got := count(`SELECT COUNT(*) FROM events WHERE kind IN ('old.open', 'new.acked')`); got != 2 {
		t.Errorf("open or recent events pruned: %d left", got)
	}
}

func openStore(t *testing.T) *Store {
	t.Helper()
	c, _ := NewCrypto(bytes.Repeat([]byte{4}, 32))
	s, err := Open(t.TempDir(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
