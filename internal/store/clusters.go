package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"go.yaml.in/yaml/v4"
)

var ErrNotFound = errors.New("not found")

type ClusterRow struct {
	Name        string `json:"name"`
	Spec        []byte `json:"-"`
	SchematicID string `json:"schematicId"`
	State       string `json:"state"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

type ClusterSecrets struct {
	SecretsBundle []byte // secrets.yaml
	Talosconfig   []byte
	Kubeconfig    []byte // nil until bootstrapped
}

func (s *Store) PutCluster(ctx context.Context, c ClusterRow) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO clusters (name, spec, schematic_id, state) VALUES (?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET spec = excluded.spec, schematic_id = excluded.schematic_id,
			state = excluded.state, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')`,
		c.Name, string(c.Spec), c.SchematicID, c.State)
	return s.done(err, Change{Table: "clusters", Cluster: c.Name, Key: c.Name, Op: "put"})
}

// ClusterVIPs maps each stored cluster's control-plane VIP to its name. A VIP answers
// on :50000 like a machine; discovery must not record it as one.
func (s *Store) ClusterVIPs(ctx context.Context) map[string]string {
	out := map[string]string{}
	rows, err := s.ListClusters(ctx)
	if err != nil {
		return out
	}
	for _, r := range rows {
		var c struct {
			Spec struct {
				ControlPlane struct {
					VIP string `yaml:"vip"`
				} `yaml:"controlPlane"`
			} `yaml:"spec"`
		}
		if yaml.Unmarshal(r.Spec, &c) == nil && c.Spec.ControlPlane.VIP != "" {
			out[c.Spec.ControlPlane.VIP] = r.Name
		}
	}
	return out
}

func (s *Store) GetCluster(ctx context.Context, name string) (*ClusterRow, error) {
	var c ClusterRow
	var spec string
	err := s.db.QueryRowContext(ctx, `SELECT name, spec, schematic_id, state, created_at, updated_at FROM clusters WHERE name = ?`, name).
		Scan(&c.Name, &spec, &c.SchematicID, &c.State, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("cluster %q: %w", name, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	c.Spec = []byte(spec)
	return &c, nil
}

func (s *Store) ListClusters(ctx context.Context) ([]ClusterRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, spec, schematic_id, state, created_at, updated_at FROM clusters ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ClusterRow
	for rows.Next() {
		var c ClusterRow
		var spec string
		if err := rows.Scan(&c.Name, &spec, &c.SchematicID, &c.State, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		c.Spec = []byte(spec)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) SetClusterState(ctx context.Context, name, state string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE clusters SET state = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE name = ?`, state, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("cluster %q: %w", name, ErrNotFound)
	}
	s.notify(Change{Table: "clusters", Cluster: name, Key: name, Op: "put"})
	return nil
}

// DeleteCluster drops the cluster row and releases its machines: they keep running
// Talos ("configured") but belong to nobody Kubit knows, so a later scan that finds
// them in maintenance mode can offer them again.
func (s *Store) DeleteCluster(ctx context.Context, name string) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE machines SET cluster = NULL, hostname = '', pool = '', role = '', machine_config = NULL, state = 'configured', updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE cluster = ?`, name); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE events SET acked = 1 WHERE cluster = ? AND acked = 0`, name); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM clusters WHERE name = ?`, name)
	if err == nil {
		s.notify(Change{Table: "machines", Cluster: name, Op: "put"})
	}
	return s.done(err, Change{Table: "clusters", Cluster: name, Key: name, Op: "delete"})
}

func (s *Store) PutClusterSecrets(ctx context.Context, name string, sec ClusterSecrets) error {
	bundle, err := s.crypto.Seal(sec.SecretsBundle)
	if err != nil {
		return err
	}
	tc, err := s.crypto.Seal(sec.Talosconfig)
	if err != nil {
		return err
	}
	var kc []byte
	if sec.Kubeconfig != nil {
		if kc, err = s.crypto.Seal(sec.Kubeconfig); err != nil {
			return err
		}
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO cluster_secrets (cluster, secrets_bundle, talosconfig, kubeconfig) VALUES (?, ?, ?, ?)
		ON CONFLICT(cluster) DO UPDATE SET secrets_bundle = excluded.secrets_bundle,
			talosconfig = excluded.talosconfig, kubeconfig = excluded.kubeconfig`,
		name, bundle, tc, kc)
	return err
}

func (s *Store) SetKubeconfig(ctx context.Context, name string, kubeconfig []byte) error {
	kc, err := s.crypto.Seal(kubeconfig)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE cluster_secrets SET kubeconfig = ? WHERE cluster = ?`, kc, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("secrets for cluster %q: %w", name, ErrNotFound)
	}
	return nil
}

// SetTalosconfig replaces the stored admin talosconfig (certificate rotation).
func (s *Store) SetTalosconfig(ctx context.Context, name string, talosconfig []byte) error {
	tc, err := s.crypto.Seal(talosconfig)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE cluster_secrets SET talosconfig = ? WHERE cluster = ?`, tc, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("secrets for cluster %q: %w", name, ErrNotFound)
	}
	return nil
}

func (s *Store) GetClusterSecrets(ctx context.Context, name string) (*ClusterSecrets, error) {
	var bundle, tc, kc []byte
	err := s.db.QueryRowContext(ctx, `SELECT secrets_bundle, talosconfig, kubeconfig FROM cluster_secrets WHERE cluster = ?`, name).Scan(&bundle, &tc, &kc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("secrets for cluster %q: %w", name, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	var out ClusterSecrets
	if out.SecretsBundle, err = s.crypto.Open(bundle); err != nil {
		return nil, fmt.Errorf("unseal secrets bundle: %w", err)
	}
	if out.Talosconfig, err = s.crypto.Open(tc); err != nil {
		return nil, fmt.Errorf("unseal talosconfig: %w", err)
	}
	if kc != nil {
		if out.Kubeconfig, err = s.crypto.Open(kc); err != nil {
			return nil, fmt.Errorf("unseal kubeconfig: %w", err)
		}
	}
	return &out, nil
}

// PlatformStatus records the last OpenTofu run for the in-cluster layer.
type PlatformStatus struct {
	AppliedAt string            `json:"appliedAt,omitempty"`
	Outputs   map[string]string `json:"outputs,omitempty"`
	Error     string            `json:"error,omitempty"`
}

func (s *Store) SetPlatformStatus(ctx context.Context, name string, p PlatformStatus) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE clusters SET platform = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE name = ?`, string(b), name)
	return s.done(err, Change{Table: "clusters", Cluster: name, Key: name, Op: "put"})
}

func (s *Store) GetPlatformStatus(ctx context.Context, name string) (*PlatformStatus, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT platform FROM clusters WHERE name = ?`, name).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("cluster %q: %w", name, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	var p PlatformStatus
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, err
	}
	return &p, nil
}
