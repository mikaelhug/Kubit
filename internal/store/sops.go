package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type SOPSKey struct {
	Cluster   string `json:"cluster"`
	Identity  []byte `json:"-"`
	Recipient string `json:"recipient"`
	CreatedAt string `json:"createdAt"`
}

func (s *Store) GetSOPSKey(ctx context.Context, cluster string) (*SOPSKey, error) {
	k := SOPSKey{Cluster: cluster}
	var sealed []byte
	err := s.db.QueryRowContext(ctx, `SELECT identity, recipient, created_at FROM sops_keys WHERE cluster = ?`, cluster).Scan(&sealed, &k.Recipient, &k.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("sops key for cluster %q: %w", cluster, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	if k.Identity, err = s.crypto.Open(sealed); err != nil {
		return nil, fmt.Errorf("unseal sops key: %w", err)
	}
	return &k, nil
}

func (s *Store) PutSOPSKey(ctx context.Context, cluster string, identity []byte, recipient string) error {
	sealed, err := s.crypto.Seal(identity)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO sops_keys (cluster, identity, recipient) VALUES (?, ?, ?)
		ON CONFLICT(cluster) DO UPDATE SET identity = excluded.identity, recipient = excluded.recipient,
			created_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')`,
		cluster, sealed, recipient)
	return s.done(err, Change{Table: "sops", Cluster: cluster, Key: cluster, Op: "put"})
}

func (s *Store) DeleteSOPSKey(ctx context.Context, cluster string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sops_keys WHERE cluster = ?`, cluster)
	return s.done(err, Change{Table: "sops", Cluster: cluster, Key: cluster, Op: "delete"})
}
