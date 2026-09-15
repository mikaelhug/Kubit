package store

import (
	"context"
	"strconv"
	"time"
)

type Sample struct {
	TS        string `json:"ts"`
	Node      string `json:"node,omitempty"`
	CPUMilli  int64  `json:"cpuMilli"`
	CPUCap    int64  `json:"cpuCap"`
	MemBytes  int64  `json:"memBytes"`
	MemCap    int64  `json:"memCap"`
	Pods      int    `json:"pods"`
	Ready     bool   `json:"ready"`
	Reachable bool   `json:"reachable"`
}

func (s *Store) AddSamples(ctx context.Context, cluster string, ts time.Time, samples []Sample) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stamp := ts.UTC().Format(time.RFC3339)
	for _, sm := range samples {
		if _, err := tx.ExecContext(ctx, `INSERT INTO samples (ts, cluster, node, cpu_milli, cpu_cap, mem, mem_cap, pods, ready, reachable) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			stamp, cluster, sm.Node, sm.CPUMilli, sm.CPUCap, sm.MemBytes, sm.MemCap, sm.Pods, b2i(sm.Ready), b2i(sm.Reachable)); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// Samples returns rows newer than since for a cluster (node "" = totals).
func (s *Store) Samples(ctx context.Context, cluster, node string, since time.Time) ([]Sample, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT ts, node, cpu_milli, cpu_cap, mem, mem_cap, pods, ready, reachable FROM samples WHERE cluster = ? AND node = ? AND ts >= ? ORDER BY ts`, cluster, node, since.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Sample{}
	for rows.Next() {
		var sm Sample
		var ready, reach int
		if err := rows.Scan(&sm.TS, &sm.Node, &sm.CPUMilli, &sm.CPUCap, &sm.MemBytes, &sm.MemCap, &sm.Pods, &ready, &reach); err != nil {
			return nil, err
		}
		sm.Ready, sm.Reachable = ready == 1, reach == 1
		out = append(out, sm)
	}
	return out, rows.Err()
}

// PruneSamples keeps every sample for 24 h, one per hour for 30 d, nothing older.
func (s *Store) PruneSamples(ctx context.Context) error {
	day := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)
	month := time.Now().Add(-30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	if _, err := s.db.ExecContext(ctx, `DELETE FROM samples WHERE ts < ? AND strftime('%M', ts) != '00'`, day); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM samples WHERE ts < ?`, month)
	return err
}

type EventRow struct {
	ID       int64  `json:"id"`
	TS       string `json:"ts"`
	Cluster  string `json:"cluster"`
	Node     string `json:"node,omitempty"`
	Severity string `json:"severity"`
	Kind     string `json:"kind"`
	Message  string `json:"message"`
	Acked    bool   `json:"acked"`
}

func (s *Store) AddEvent(ctx context.Context, e EventRow) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO events (cluster, node, severity, kind, message) VALUES (?, ?, ?, ?, ?)`, e.Cluster, e.Node, e.Severity, e.Kind, e.Message)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) Events(ctx context.Context, cluster string, limit int, unackedOnly bool) ([]EventRow, error) {
	q := `SELECT id, ts, cluster, node, severity, kind, message, acked FROM events WHERE cluster = ?`
	if unackedOnly {
		q += ` AND acked = 0`
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY id DESC LIMIT ?`, cluster, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EventRow{}
	for rows.Next() {
		var e EventRow
		var acked int
		if err := rows.Scan(&e.ID, &e.TS, &e.Cluster, &e.Node, &e.Severity, &e.Kind, &e.Message, &acked); err != nil {
			return nil, err
		}
		e.Acked = acked == 1
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) AckEvent(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE events SET acked = 1 WHERE id = ?`, id)
	return s.done(err, Change{Table: "events", Key: strconv.FormatInt(id, 10), Op: "ack"})
}

func (s *Store) AckClusterEvents(ctx context.Context, cluster string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE events SET acked = 1 WHERE cluster = ?`, cluster)
	return s.done(err, Change{Table: "events", Cluster: cluster, Key: "*", Op: "ack"})
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// HasOpenEvent reports whether an unacknowledged event of this kind exists.
func (s *Store) HasOpenEvent(ctx context.Context, cluster, node, kind string) bool {
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE cluster = ? AND node = ? AND kind = ? AND acked = 0`, cluster, node, kind).Scan(&n)
	return n > 0
}

// ResolveEvents acknowledges open events of one kind for a node: the condition cleared.
func (s *Store) ResolveEvents(ctx context.Context, cluster, node, kind string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE events SET acked = 1 WHERE cluster = ? AND node = ? AND kind = ? AND acked = 0`, cluster, node, kind)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.notify(Change{Table: "events", Cluster: cluster, Key: kind, Node: node, Op: "resolve"})
	}
	return nil
}
