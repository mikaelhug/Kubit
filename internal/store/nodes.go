package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type NodeRow struct {
	IP           string `json:"ip"`
	Cluster      string `json:"cluster"` // "" when unassigned
	Hostname     string `json:"hostname"`
	MAC          string `json:"mac"`
	Arch         string `json:"arch"`
	Role         string `json:"role"`
	Source       string `json:"source"`
	State        string `json:"state"`
	Hardware     []byte `json:"-"` // JSON inventory
	TalosVersion string `json:"talosVersion"`
	LastSeen     string `json:"lastSeen"`
	UpdatedAt    string `json:"updatedAt"`
}

const nodeCols = `ip, COALESCE(cluster,''), hostname, mac, arch, role, source, state, hardware, talos_version, COALESCE(last_seen,''), updated_at`

func scanNode(sc interface{ Scan(...any) error }) (*NodeRow, error) {
	var n NodeRow
	var hw string
	if err := sc.Scan(&n.IP, &n.Cluster, &n.Hostname, &n.MAC, &n.Arch, &n.Role, &n.Source, &n.State, &hw, &n.TalosVersion, &n.LastSeen, &n.UpdatedAt); err != nil {
		return nil, err
	}
	n.Hardware = []byte(hw)
	return &n, nil
}

// UpsertNode writes discovery results; cluster membership fields are preserved when the
// incoming row leaves them empty so a rescan never detaches a node.
func (s *Store) UpsertNode(ctx context.Context, n NodeRow) error {
	var cluster any
	if n.Cluster != "" {
		cluster = n.Cluster
	}
	hw := string(n.Hardware)
	if hw == "" {
		hw = "{}"
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO nodes (ip, cluster, hostname, mac, arch, role, source, state, hardware, talos_version, last_seen)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		ON CONFLICT(ip) DO UPDATE SET
			cluster       = COALESCE(excluded.cluster, nodes.cluster),
			hostname      = CASE WHEN excluded.hostname = '' THEN nodes.hostname ELSE excluded.hostname END,
			mac           = CASE WHEN excluded.mac = '' THEN nodes.mac ELSE excluded.mac END,
			arch          = CASE WHEN excluded.arch = '' THEN nodes.arch ELSE excluded.arch END,
			role          = CASE WHEN excluded.role = '' THEN nodes.role ELSE excluded.role END,
			source        = excluded.source,
			state         = excluded.state,
			hardware      = CASE WHEN excluded.hardware = '{}' THEN nodes.hardware ELSE excluded.hardware END,
			talos_version = CASE WHEN excluded.talos_version = '' THEN nodes.talos_version ELSE excluded.talos_version END,
			last_seen     = excluded.last_seen,
			updated_at    = strftime('%Y-%m-%dT%H:%M:%fZ','now')`,
		n.IP, cluster, n.Hostname, n.MAC, n.Arch, n.Role, n.Source, n.State, hw, n.TalosVersion)
	return err
}

func (s *Store) GetNode(ctx context.Context, ip string) (*NodeRow, error) {
	n, err := scanNode(s.db.QueryRowContext(ctx, `SELECT `+nodeCols+` FROM nodes WHERE ip = ?`, ip))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("node %s: %w", ip, ErrNotFound)
	}
	return n, err
}

// ListNodes returns every node, or only a cluster's when cluster is non-empty.
func (s *Store) ListNodes(ctx context.Context, cluster string) ([]NodeRow, error) {
	q := `SELECT ` + nodeCols + ` FROM nodes`
	var args []any
	if cluster != "" {
		q += ` WHERE cluster = ?`
		args = append(args, cluster)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY cluster, role, hostname, ip`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NodeRow
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

func (s *Store) SetNodeState(ctx context.Context, ip, state string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET state = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE ip = ?`, state, ip)
	return err
}

func (s *Store) AssignNode(ctx context.Context, ip, cluster, hostname, role string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET cluster = ?, hostname = ?, role = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE ip = ?`, cluster, hostname, role, ip)
	return err
}

func (s *Store) PutNodeMachineConfig(ctx context.Context, ip string, cfg []byte) error {
	sealed, err := s.crypto.Seal(cfg)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE nodes SET machine_config = ? WHERE ip = ?`, sealed, ip)
	return err
}

func (s *Store) GetNodeMachineConfig(ctx context.Context, ip string) ([]byte, error) {
	var sealed []byte
	err := s.db.QueryRowContext(ctx, `SELECT machine_config FROM nodes WHERE ip = ?`, ip).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && sealed == nil) {
		return nil, fmt.Errorf("machine config for %s: %w", ip, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	return s.crypto.Open(sealed)
}

func (s *Store) DeleteNode(ctx context.Context, ip string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM nodes WHERE ip = ?`, ip)
	return err
}

// UnassignNode detaches a node from its cluster after a reset, keeping the inventory.
func (s *Store) UnassignNode(ctx context.Context, ip, state string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET cluster = NULL, hostname = '', role = '', state = ?, machine_config = NULL, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE ip = ?`, state, ip)
	return err
}
