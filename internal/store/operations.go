package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"time"
)

type OperationRow struct {
	ID         int64           `json:"id"`
	Cluster    string          `json:"cluster"`
	Kind       string          `json:"kind"`
	Status     string          `json:"status"`
	Log        string          `json:"log"`
	StartedAt  string          `json:"startedAt"`
	FinishedAt string          `json:"finishedAt,omitempty"`
	Steps      json.RawMessage `json:"steps"`
	Artifact   json.RawMessage `json:"artifact,omitempty"`
	Request    json.RawMessage `json:"request,omitempty"`
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
	return s.done(err, Change{Table: "operations", Key: strconv.FormatInt(id, 10), Op: "put"})
}

func (s *Store) AppendOperationLog(ctx context.Context, id int64, line string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE operations SET log = log || ? || char(10) WHERE id = ?`, line, id)
	return err
}

func (s *Store) FinishOperation(ctx context.Context, id int64, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE operations SET status = ?, finished_at = `+sqlNow+` WHERE id = ?`, status, id)
	return err
}

const operationCols = `id, cluster, kind, status, started_at, finished_at, steps`

func scanOperation(sc interface{ Scan(...any) error }, more ...any) (*OperationRow, error) {
	var o OperationRow
	var finished sql.NullString
	var steps string
	if err := sc.Scan(append([]any{&o.ID, &o.Cluster, &o.Kind, &o.Status, &o.StartedAt, &finished, &steps}, more...)...); err != nil {
		return nil, err
	}
	o.FinishedAt = finished.String
	o.Steps = rawOrNull(steps, "[]")
	return &o, nil
}

func (s *Store) GetOperation(ctx context.Context, id int64) (*OperationRow, error) {
	var log, artifact, request string
	o, err := scanOperation(s.db.QueryRowContext(ctx, `SELECT `+operationCols+`, log, artifact, request FROM operations WHERE id = ?`, id), &log, &artifact, &request)
	if err := notFound(err, "operation %d", id); err != nil {
		return nil, err
	}
	o.Log = log
	o.Artifact = rawOrNull(artifact, "")
	o.Request = rawOrNull(request, "")
	return o, nil
}

func (s *Store) LastFinished(ctx context.Context, cluster string, kinds []string) time.Time {
	var out time.Time
	for _, k := range kinds {
		var ts sql.NullString
		if err := s.db.QueryRowContext(ctx, `SELECT finished_at FROM operations WHERE kind = ? AND finished_at IS NOT NULL AND (cluster = ? OR EXISTS (SELECT 1 FROM json_each(CASE WHEN json_valid(request) THEN request ELSE '{}' END, '$.clusters') WHERE value = ?)) ORDER BY id DESC LIMIT 1`, k, cluster, cluster).Scan(&ts); err == nil && ts.Valid {
			if t, err := time.Parse(time.RFC3339Nano, ts.String); err == nil && t.After(out) {
				out = t
			}
		}
	}
	return out
}

func (s *Store) ListOperations(ctx context.Context, limit int) ([]OperationRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+operationCols+` FROM operations ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OperationRow
	for rows.Next() {
		o, err := scanOperation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *o)
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

func (s *Store) MarkStaleOperations(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE operations SET status = 'failed', finished_at = `+sqlNow+`, log = log || 'kubit restarted while this operation was running' || char(10) WHERE status = 'running'`)
	return err
}
