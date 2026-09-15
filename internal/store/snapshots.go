package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Snapshot is one etcd snapshot taken through the Talos API and kept sealed on disk.
type Snapshot struct {
	ID           int64  `json:"id"`
	Cluster      string `json:"cluster"`
	TS           string `json:"ts"`
	Node         string `json:"node"`
	Path         string `json:"-"`
	SizeBytes    int64  `json:"sizeBytes"`
	SHA256       string `json:"sha256"`
	Keys         int64  `json:"keys"`
	TalosVersion string `json:"talosVersion,omitempty"`
	K8sVersion   string `json:"k8sVersion,omitempty"`
	Source       string `json:"source"`
	Status       string `json:"status"`
	Offsite      string `json:"offsite,omitempty"` // remote key once copied
}

func (s *Store) AddSnapshot(ctx context.Context, sn Snapshot) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO snapshots (cluster, node, path, size_bytes, sha256, keys, talos_version, k8s_version, source, status) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sn.Cluster, sn.Node, sn.Path, sn.SizeBytes, sn.SHA256, sn.Keys, sn.TalosVersion, sn.K8sVersion, sn.Source, sn.Status)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

const snapshotCols = `id, cluster, ts, node, path, size_bytes, sha256, keys, talos_version, k8s_version, source, status, offsite`

func scanSnapshot(r interface{ Scan(...any) error }) (*Snapshot, error) {
	var sn Snapshot
	if err := r.Scan(&sn.ID, &sn.Cluster, &sn.TS, &sn.Node, &sn.Path, &sn.SizeBytes, &sn.SHA256, &sn.Keys, &sn.TalosVersion, &sn.K8sVersion, &sn.Source, &sn.Status, &sn.Offsite); err != nil {
		return nil, err
	}
	return &sn, nil
}

// ListSnapshots returns a cluster's snapshots, newest first.
func (s *Store) ListSnapshots(ctx context.Context, cluster string) ([]Snapshot, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+snapshotCols+` FROM snapshots WHERE cluster = ? ORDER BY ts DESC, id DESC`, cluster)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Snapshot{}
	for rows.Next() {
		sn, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sn)
	}
	return out, rows.Err()
}

func (s *Store) GetSnapshot(ctx context.Context, id int64) (*Snapshot, error) {
	sn, err := scanSnapshot(s.db.QueryRowContext(ctx, `SELECT `+snapshotCols+` FROM snapshots WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("snapshot %d: %w", id, ErrNotFound)
	}
	return sn, err
}

func (s *Store) SetSnapshotOffsite(ctx context.Context, id int64, key string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE snapshots SET offsite = ? WHERE id = ?`, key, id)
	return err
}

func (s *Store) SetSnapshotStatus(ctx context.Context, id int64, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE snapshots SET status = ? WHERE id = ?`, status, id)
	return err
}

func (s *Store) DeleteSnapshot(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM snapshots WHERE id = ?`, id)
	return err
}

// LatestSnapshotTS returns the newest snapshot time for a cluster ("" when none).
func (s *Store) LatestSnapshotTS(ctx context.Context, cluster string) (string, error) {
	var ts sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT MAX(ts) FROM snapshots WHERE cluster = ? AND status = 'ok'`, cluster).Scan(&ts)
	return ts.String, err
}

// SealFile and OpenFile expose the store's master-key crypto for large artefacts kept
// outside the database (etcd snapshots).
func (s *Store) SealFile(plain []byte) ([]byte, error)  { return s.crypto.Seal(plain) }
func (s *Store) OpenFile(sealed []byte) ([]byte, error) { return s.crypto.Open(sealed) }
