package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type OperationRow struct {
	ID         int64  `json:"id"`
	Cluster    string `json:"cluster"`
	Kind       string `json:"kind"`
	Status     string `json:"status"` // running | done | failed
	Log        string `json:"log"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt,omitempty"`
}

func (s *Store) CreateOperation(ctx context.Context, cluster, kind string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO operations (cluster, kind) VALUES (?, ?)`, cluster, kind)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) AppendOperationLog(ctx context.Context, id int64, line string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE operations SET log = log || ? || char(10) WHERE id = ?`, line, id)
	return err
}

func (s *Store) FinishOperation(ctx context.Context, id int64, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE operations SET status = ?, finished_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`, status, id)
	return err
}

func (s *Store) GetOperation(ctx context.Context, id int64) (*OperationRow, error) {
	var o OperationRow
	var finished sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id, cluster, kind, status, log, started_at, finished_at FROM operations WHERE id = ?`, id).
		Scan(&o.ID, &o.Cluster, &o.Kind, &o.Status, &o.Log, &o.StartedAt, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("operation %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	o.FinishedAt = finished.String
	return &o, nil
}

// ListOperations returns the most recent operations first, without their logs.
func (s *Store) ListOperations(ctx context.Context, limit int) ([]OperationRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, cluster, kind, status, started_at, finished_at FROM operations ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OperationRow
	for rows.Next() {
		var o OperationRow
		var finished sql.NullString
		if err := rows.Scan(&o.ID, &o.Cluster, &o.Kind, &o.Status, &o.StartedAt, &finished); err != nil {
			return nil, err
		}
		o.FinishedAt = finished.String
		out = append(out, o)
	}
	return out, rows.Err()
}

// MarkStaleOperations flips operations left "running" by a previous process to failed.
func (s *Store) MarkStaleOperations(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE operations SET status = 'failed', finished_at = strftime('%Y-%m-%dT%H:%M:%fZ','now'), log = log || 'kubit restarted while this operation was running' || char(10) WHERE status = 'running'`)
	return err
}
