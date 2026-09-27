package store

import (
	"strings"
	"testing"
	"time"
)

func TestHotQueriesUseTheirIndexes(t *testing.T) {
	s := openLab(t)
	ctx := t.Context()
	for _, c := range []struct {
		query string
		args  []any
		index string
	}{
		{`SELECT COUNT(*) FROM events WHERE cluster = ? AND node = ? AND kind = ? AND acked = 0`, []any{"c", "n", "k"}, "events_open"},
		{`SELECT COUNT(*) FROM events WHERE cluster = ? AND acked = 0 AND severity IN ('warn', 'critical')`, []any{"c"}, "events_open"},
		{`UPDATE events SET acked = 1 WHERE cluster = ? AND node = ? AND kind = ? AND acked = 0`, []any{"c", "n", "k"}, "events_open"},
		{`SELECT id FROM operations WHERE cluster = ? AND kind = ? AND status = ? ORDER BY id DESC LIMIT 1`, []any{"c", "k", "done"}, "operations_kind"},
		{`SELECT finished_at, MAX(id) FROM operations WHERE kind IN (?, ?) AND finished_at IS NOT NULL AND (cluster = ? OR EXISTS (SELECT 1 FROM json_each(CASE WHEN json_valid(request) THEN request ELSE '{}' END, '$.clusters') WHERE value = ?)) GROUP BY kind`, []any{"a", "b", "c", "c"}, "operations_kind"},
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
	s := openLab(t)
	var mode int
	if err := s.db.QueryRowContext(t.Context(), `PRAGMA synchronous`).Scan(&mode); err != nil || mode != 1 {
		t.Errorf("synchronous = %d (%v), want 1 (NORMAL)", mode, err)
	}
}

func TestPruneKeepsRecentHistory(t *testing.T) {
	s := openLab(t)
	ctx := t.Context()
	old := time.Now().Add(-100 * 24 * time.Hour).UTC().Format(time.RFC3339)
	recent := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	ancient := time.Now().Add(-400 * 24 * time.Hour).UTC().Format(time.RFC3339)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO events (ts, cluster, severity, kind, message, acked) VALUES (?, 'c', 'warn', 'old.acked', '', 1), (?, 'c', 'warn', 'old.open', '', 0), (?, 'c', 'warn', 'new.acked', '', 1)`, old, old, recent)
	for i := 0; i < operationsKept+2; i++ {
		exec(`INSERT INTO operations (cluster, kind, status, started_at, finished_at) VALUES ('a', 'x', 'done', ?, ?)`, old, old)
	}
	exec(`INSERT INTO operations (cluster, kind, status, started_at, finished_at) VALUES ('b', 'x', 'done', ?, ?)`, old, old)
	exec(`INSERT INTO operations (cluster, kind, status, started_at) VALUES ('a', 'x', 'running', ?)`, old)
	exec(`INSERT INTO operation_log (op_id, seq, line) SELECT id, 1, 'line' FROM operations`)
	exec(`INSERT INTO audit_log (at, action) VALUES (?, 'ancient'), (?, 'old')`, ancient, old)
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
	for q, want := range map[string]int{
		`SELECT COUNT(*) FROM operations WHERE cluster = 'a' AND finished_at IS NOT NULL`:   operationsKept - 1,
		`SELECT COUNT(*) FROM operations WHERE cluster = 'b'`:                               1,
		`SELECT COUNT(*) FROM operations WHERE status = 'running'`:                          1,
		`SELECT COUNT(*) FROM operation_log WHERE op_id NOT IN (SELECT id FROM operations)`: 0,
		`SELECT COUNT(*) FROM operation_log`:                                                operationsKept + 1,
		`SELECT COUNT(*) FROM audit_log WHERE action = 'ancient'`:                           0,
		`SELECT COUNT(*) FROM audit_log WHERE action = 'old'`:                               1,
		`SELECT MIN(id) FROM operations WHERE cluster = 'a'`:                                4,
	} {
		if got := count(q); got != want {
			t.Errorf("%s = %d, want %d", q, got, want)
		}
	}
}
