package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

type OperationRow struct {
	ID         int64  `json:"id"`
	Cluster    string `json:"cluster"`
	Kind       string `json:"kind"`
	Status     string `json:"status"` // running | done | failed | cancelled
	Log        string `json:"log"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt,omitempty"`
	// Steps is the JSON step list (see cluster.Step); Artifact is kind-specific JSON
	// (a plan diff for platform.plan); Request is the JSON body that started it, for retry.
	Steps    json.RawMessage `json:"steps"`
	Artifact json.RawMessage `json:"artifact,omitempty"`
	Request  json.RawMessage `json:"request,omitempty"`
}

func (s *Store) CreateOperation(ctx context.Context, cluster, kind string, request []byte) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO operations (cluster, kind, request) VALUES (?, ?, ?)`, cluster, kind, string(request))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) SetOperationSteps(ctx context.Context, id int64, steps []byte) error {
	_, err := s.db.ExecContext(ctx, `UPDATE operations SET steps = ? WHERE id = ?`, string(steps), id)
	return err
}

func (s *Store) SetOperationArtifact(ctx context.Context, id int64, artifact []byte) error {
	_, err := s.db.ExecContext(ctx, `UPDATE operations SET artifact = ? WHERE id = ?`, string(artifact), id)
	return err
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
	var steps, artifact, request string
	err := s.db.QueryRowContext(ctx, `SELECT id, cluster, kind, status, log, started_at, finished_at, steps, artifact, request FROM operations WHERE id = ?`, id).
		Scan(&o.ID, &o.Cluster, &o.Kind, &o.Status, &o.Log, &o.StartedAt, &finished, &steps, &artifact, &request)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("operation %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	o.FinishedAt = finished.String
	o.Steps = rawOrNull(steps, "[]")
	o.Artifact = rawOrNull(artifact, "")
	o.Request = rawOrNull(request, "")
	return &o, nil
}

// ListOperations returns the most recent operations first, without their logs.
func (s *Store) ListOperations(ctx context.Context, limit int) ([]OperationRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, cluster, kind, status, started_at, finished_at, steps FROM operations ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OperationRow
	for rows.Next() {
		var o OperationRow
		var finished sql.NullString
		var steps string
		if err := rows.Scan(&o.ID, &o.Cluster, &o.Kind, &o.Status, &o.StartedAt, &finished, &steps); err != nil {
			return nil, err
		}
		o.FinishedAt = finished.String
		o.Steps = rawOrNull(steps, "[]")
		out = append(out, o)
	}
	return out, rows.Err()
}

func rawOrNull(v, fallback string) json.RawMessage {
	if v == "" {
		if fallback == "" {
			return nil
		}
		return json.RawMessage(fallback)
	}
	return json.RawMessage(v)
}

// MarkStaleOperations flips operations left "running" by a previous process to failed.
func (s *Store) MarkStaleOperations(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE operations SET status = 'failed', finished_at = strftime('%Y-%m-%dT%H:%M:%fZ','now'), log = log || 'kubit restarted while this operation was running' || char(10) WHERE status = 'running'`)
	return err
}
