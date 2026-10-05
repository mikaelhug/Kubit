package store

import (
	"context"
	"database/sql"
	"errors"
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
	Disk      int64  `json:"disk,omitempty"`
	DiskCap   int64  `json:"diskCap,omitempty"`
}

const KubitKey = "kubit"

func (s *Store) AddSamples(ctx context.Context, cluster string, ts time.Time, samples []Sample) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stamp := ts.UTC().Format(time.RFC3339)
	for _, sm := range samples {
		if _, err := tx.ExecContext(ctx, `INSERT INTO samples (ts, cluster, node, cpu_milli, cpu_cap, mem, mem_cap, pods, ready, reachable, disk, disk_cap) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			stamp, cluster, sm.Node, sm.CPUMilli, sm.CPUCap, sm.MemBytes, sm.MemCap, sm.Pods, b2i(sm.Ready), b2i(sm.Reachable), sm.Disk, sm.DiskCap); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func scanSample(sc scanner) (*Sample, error) {
	var sm Sample
	var ready, reach int
	if err := sc.Scan(&sm.TS, &sm.Node, &sm.CPUMilli, &sm.CPUCap, &sm.MemBytes, &sm.MemCap, &sm.Pods, &ready, &reach, &sm.Disk, &sm.DiskCap); err != nil {
		return nil, err
	}
	sm.Ready, sm.Reachable = ready == 1, reach == 1
	return &sm, nil
}

func (s *Store) Samples(ctx context.Context, cluster, node string, since time.Time) ([]Sample, error) {
	return queryAll(ctx, s.db, scanSample, `SELECT ts, node, cpu_milli, cpu_cap, mem, mem_cap, pods, ready, reachable, disk, disk_cap FROM samples WHERE cluster = ? AND node = ? AND ts >= ? ORDER BY ts`, cluster, node, since.UTC().Format(time.RFC3339))
}

const eventRetention = 90 * 24 * time.Hour

func (s *Store) Prune(ctx context.Context) error {
	now := time.Now()
	before := func(d time.Duration) string { return now.Add(-d).UTC().Format(time.RFC3339) }
	for _, p := range []struct {
		query string
		args  []any
	}{
		{`DELETE FROM samples WHERE ts < ? AND strftime('%M', ts) != '00'`, []any{before(24 * time.Hour)}},
		{`DELETE FROM samples WHERE ts < ?`, []any{before(30 * 24 * time.Hour)}},
		{`DELETE FROM events WHERE acked = 1 AND ts < ?`, []any{before(eventRetention)}},
	} {
		if _, err := s.db.ExecContext(ctx, p.query, p.args...); err != nil {
			return err
		}
	}
	return nil
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

const selectEvents = `SELECT id, ts, cluster, node, severity, kind, message, acked FROM events WHERE cluster = ?`

func (s *Store) Events(ctx context.Context, cluster string, limit int, unackedOnly bool) ([]EventRow, error) {
	q := selectEvents
	if unackedOnly {
		q += ` AND acked = 0`
	}
	return queryAll(ctx, s.db, scanEvent, q+` ORDER BY id DESC LIMIT ?`, cluster, limit)
}

func (s *Store) OpenWorkloadEvents(ctx context.Context, cluster string, limit int) ([]EventRow, error) {
	return queryAll(ctx, s.db, scanEvent, selectEvents+` AND acked = 0 AND instr(node, '/') > 0 ORDER BY id DESC LIMIT ?`, cluster, limit)
}

func (s *Store) OpenAlerts(ctx context.Context, cluster string) ([]EventRow, error) {
	return queryAll(ctx, s.db, scanEvent, selectEvents+` AND acked = 0 AND severity != 'info' ORDER BY id DESC`, cluster)
}

func scanEvent(sc scanner) (*EventRow, error) {
	var e EventRow
	var acked int
	if err := sc.Scan(&e.ID, &e.TS, &e.Cluster, &e.Node, &e.Severity, &e.Kind, &e.Message, &acked); err != nil {
		return nil, err
	}
	e.Acked = acked == 1
	return &e, nil
}

func (s *Store) AckEvent(ctx context.Context, id int64) error {
	var cluster string
	err := s.db.QueryRowContext(ctx, `UPDATE events SET acked = 1 WHERE id = ? RETURNING cluster`, id).Scan(&cluster)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return s.done(err, Change{Table: "events", Cluster: cluster, Key: strconv.FormatInt(id, 10), Op: "ack"})
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

func (s *Store) HasOpenEvent(ctx context.Context, cluster, node, kind string) bool {
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE cluster = ? AND node = ? AND kind = ? AND acked = 0`, cluster, node, kind).Scan(&n)
	return n > 0
}

func (s *Store) OpenEventCount(ctx context.Context, cluster string) int {
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE cluster = ? AND acked = 0 AND severity IN ('warn', 'critical')`, cluster).Scan(&n)
	return n
}

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
