package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
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

const appendLog = `INSERT INTO operation_log (op_id, seq, line) SELECT id, COALESCE((SELECT MAX(seq) FROM operation_log WHERE op_id = operations.id), 0) + 1, ? FROM operations`

func (s *Store) AppendOperationLog(ctx context.Context, id int64, line string) error {
	_, err := s.db.ExecContext(ctx, appendLog+` WHERE id = ?`, line, id)
	return err
}

func (s *Store) operationLog(ctx context.Context, id int64, legacy string) (string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT line FROM operation_log WHERE op_id = ? ORDER BY seq`, id)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var b strings.Builder
	b.WriteString(legacy)
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return "", err
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String(), rows.Err()
}

func (s *Store) FinishOperation(ctx context.Context, id int64, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE operations SET status = ?, finished_at = `+sqlNow+` WHERE id = ?`, status, id)
	return err
}

const operationCols = `id, cluster, kind, status, started_at, finished_at, steps`

func scanOperation(sc scanner, more ...any) (*OperationRow, error) {
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
	return s.getOperation(ctx, id, true)
}

func (s *Store) GetOperationWithoutLog(ctx context.Context, id int64) (*OperationRow, error) {
	return s.getOperation(ctx, id, false)
}

func (s *Store) getOperation(ctx context.Context, id int64, withLog bool) (*OperationRow, error) {
	logCol := `''`
	if withLog {
		logCol = `log`
	}
	var log, artifact, request string
	o, err := scanOperation(s.db.QueryRowContext(ctx, `SELECT `+operationCols+`, `+logCol+`, artifact, request FROM operations WHERE id = ?`, id), &log, &artifact, &request)
	if err := notFound(err, "operation %d", id); err != nil {
		return nil, err
	}
	if withLog {
		if o.Log, err = s.operationLog(ctx, id, log); err != nil {
			return nil, err
		}
	}
	o.Artifact = rawOrNull(artifact, "")
	o.Request = rawOrNull(request, "")
	return o, nil
}

func (s *Store) LastFinished(ctx context.Context, cluster string, kinds []string) time.Time {
	var out time.Time
	if len(kinds) == 0 {
		return out
	}
	args := make([]any, 0, len(kinds)+2)
	for _, k := range kinds {
		args = append(args, k)
	}
	args = append(args, cluster, cluster)
	rows, err := s.db.QueryContext(ctx, `SELECT finished_at, MAX(id) FROM operations WHERE kind IN (?`+strings.Repeat(`, ?`, len(kinds)-1)+`) AND finished_at IS NOT NULL AND (cluster = ? OR EXISTS (SELECT 1 FROM json_each(CASE WHEN json_valid(request) THEN request ELSE '{}' END, '$.clusters') WHERE value = ?)) GROUP BY kind`, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var ts string
		var id int64
		if rows.Scan(&ts, &id) != nil {
			continue
		}
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil && t.After(out) {
			out = t
		}
	}
	return out
}

func (s *Store) LatestOperation(ctx context.Context, cluster, kind, status string) int64 {
	var id int64
	_ = s.db.QueryRowContext(ctx, `SELECT id FROM operations WHERE cluster = ? AND kind = ? AND status = ? ORDER BY id DESC LIMIT 1`, cluster, kind, status).Scan(&id)
	return id
}

func (s *Store) ListOperations(ctx context.Context, limit int) ([]OperationRow, error) {
	return queryAll(ctx, s.db, func(sc scanner) (*OperationRow, error) { return scanOperation(sc) }, `SELECT `+operationCols+` FROM operations ORDER BY id DESC LIMIT ?`, limit)
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, appendLog+` WHERE status = 'running'`, "kubit restarted while this operation was running"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE operations SET status = 'failed', finished_at = `+sqlNow+` WHERE status = 'running'`); err != nil {
		return err
	}
	return tx.Commit()
}
