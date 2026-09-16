package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mikael/kubit/internal/labhost"
	"github.com/mikael/kubit/internal/oob"
	"strings"
	"time"
)

// Machine is a physical or virtual computer Kubit knows, keyed by the MAC of its
// uplink. The IP is only where it was last reachable.
type Machine struct {
	MAC          string   `json:"mac"`
	UUID         string   `json:"uuid,omitempty"`
	Serial       string   `json:"serial,omitempty"`
	IP           string   `json:"ip"`
	IPsSeen      []string `json:"ipsSeen,omitempty"`
	Cluster      string   `json:"cluster"` // "" when unassigned
	Hostname     string   `json:"hostname"`
	Pool         string   `json:"pool"`
	Role         string   `json:"role"`
	Arch         string   `json:"arch"`
	Source       string   `json:"source"`
	State        string   `json:"state"`
	Hardware     []byte   `json:"-"`
	TalosVersion string   `json:"talosVersion"`
	WOL          bool     `json:"wol"`
	// OOB is the out-of-band config (password redacted on the API); OOBType is a
	// cheap "has remote management" for lists.
	OOB     *oob.Config `json:"oob,omitempty"`
	OOBType string      `json:"oobType,omitempty"`
	// LabHost is set on machines Kubit turned into KVM hosts; Host on the VMs they run.
	LabHost   *LabHost `json:"labhost,omitempty"`
	Host      string   `json:"host,omitempty"`
	FirstSeen string   `json:"firstSeen"`
	LastSeen  string   `json:"lastSeen"`
	UpdatedAt string   `json:"updatedAt"`
}

// NodeRow is the pre-M8 name; discovery and the API still speak in these terms.
type NodeRow = Machine

// MachineKey returns the identity used as primary key: the MAC, or a placeholder
// derived from the IP for declarations that never recorded one.
func MachineKey(mac, ip string) string {
	if mac != "" {
		return strings.ToLower(mac)
	}
	return "ip:" + ip
}

const machineCols = `mac, uuid, serial, COALESCE(ip,''), ips_seen, COALESCE(cluster,''), hostname, pool, role, arch, source, state, hardware, talos_version, wol, first_seen, COALESCE(last_seen,''), updated_at, oob, labhost, host`

func scanMachine(sc interface{ Scan(...any) error }) (*Machine, error) {
	var m Machine
	var hw, seen, oobRaw, lab string
	var wol int
	if err := sc.Scan(&m.MAC, &m.UUID, &m.Serial, &m.IP, &seen, &m.Cluster, &m.Hostname, &m.Pool, &m.Role, &m.Arch, &m.Source, &m.State, &hw, &m.TalosVersion, &wol, &m.FirstSeen, &m.LastSeen, &m.UpdatedAt, &oobRaw, &lab, &m.Host); err != nil {
		return nil, err
	}
	if lab != "" {
		var l LabHost
		if json.Unmarshal([]byte(lab), &l) == nil {
			if l.VMs == nil {
				l.VMs = []labhost.VM{}
			}
			m.LabHost = &l
		}
	}
	if oobRaw != "" {
		var c oob.Config
		if json.Unmarshal([]byte(oobRaw), &c) == nil {
			m.OOB = &c
			m.OOBType = c.Type
		}
	}
	m.Hardware = []byte(hw)
	m.WOL = wol == 1
	_ = json.Unmarshal([]byte(seen), &m.IPsSeen)
	return &m, nil
}

// UpsertNode records a discovery or declaration. Identity is the MAC; a machine that
// shows up on a new IP keeps its row (the old IP joins ips_seen) and any other row
// still claiming that IP loses it. Cluster membership and hardware survive rescans
// that carry neither.
func (s *Store) UpsertNode(ctx context.Context, n Machine) error {
	key := MachineKey(n.MAC, n.IP)
	if n.MAC == "" && n.IP != "" {
		// No identity given: attach to whatever machine currently holds that IP.
		if existing, err := s.GetNode(ctx, n.IP); err == nil {
			key = existing.MAC
		}
	}
	if n.IP != "" {
		if _, err := s.db.ExecContext(ctx, `UPDATE machines SET ip = NULL WHERE ip = ? AND mac != ?`, n.IP, key); err != nil {
			return err
		}
	}
	hw := string(n.Hardware)
	if hw == "" {
		hw = "{}"
	}
	prev, _ := s.GetMachine(ctx, key)
	seen := []string{}
	if prev != nil {
		seen = prev.IPsSeen
		if prev.IP != "" && prev.IP != n.IP && !contains(seen, prev.IP) {
			seen = append(seen, prev.IP)
		}
	}
	seenJSON, _ := json.Marshal(seen)
	var cluster any
	if n.Cluster != "" {
		cluster = n.Cluster
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO machines (mac, uuid, serial, ip, ips_seen, cluster, hostname, pool, role, arch, source, state, hardware, talos_version, last_seen)
		VALUES (?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		ON CONFLICT(mac) DO UPDATE SET
			uuid          = CASE WHEN excluded.uuid = '' THEN machines.uuid ELSE excluded.uuid END,
			serial        = CASE WHEN excluded.serial = '' THEN machines.serial ELSE excluded.serial END,
			ip            = COALESCE(excluded.ip, machines.ip),
			ips_seen      = excluded.ips_seen,
			-- A member found back in maintenance mode was wiped outside Kubit: it is
			-- no longer part of any cluster.
			cluster       = CASE WHEN excluded.state = 'maintenance' AND excluded.cluster IS NULL THEN NULL ELSE COALESCE(excluded.cluster, machines.cluster) END,
			hostname      = CASE WHEN excluded.state = 'maintenance' AND excluded.cluster IS NULL THEN '' WHEN excluded.hostname = '' THEN machines.hostname ELSE excluded.hostname END,
			pool          = CASE WHEN excluded.state = 'maintenance' AND excluded.cluster IS NULL THEN '' WHEN excluded.pool = '' THEN machines.pool ELSE excluded.pool END,
			role          = CASE WHEN excluded.state = 'maintenance' AND excluded.cluster IS NULL THEN '' WHEN excluded.role = '' THEN machines.role ELSE excluded.role END,
			arch          = CASE WHEN excluded.arch = '' THEN machines.arch ELSE excluded.arch END,
			source        = excluded.source,
			state         = CASE WHEN excluded.state = '' THEN machines.state ELSE excluded.state END,
			hardware      = CASE WHEN excluded.hardware = '{}' THEN machines.hardware ELSE excluded.hardware END,
			talos_version = CASE WHEN excluded.talos_version = '' THEN machines.talos_version ELSE excluded.talos_version END,
			last_seen     = excluded.last_seen,
			updated_at    = strftime('%Y-%m-%dT%H:%M:%fZ','now')`,
		key, n.UUID, n.Serial, n.IP, string(seenJSON), cluster, n.Hostname, n.Pool, n.Role, n.Arch, n.Source, n.State, hw, n.TalosVersion)
	return s.done(err, Change{Table: "machines", Cluster: n.Cluster, Key: key, Op: "put"})
}

func (s *Store) GetMachine(ctx context.Context, mac string) (*Machine, error) {
	m, err := scanMachine(s.db.QueryRowContext(ctx, `SELECT `+machineCols+` FROM machines WHERE mac = ?`, strings.ToLower(mac)))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("machine %s: %w", mac, ErrNotFound)
	}
	return m, err
}

// GetNode looks a machine up by its current IP.
func (s *Store) GetNode(ctx context.Context, ip string) (*Machine, error) {
	m, err := scanMachine(s.db.QueryRowContext(ctx, `SELECT `+machineCols+` FROM machines WHERE ip = ? OR mac = ?`, ip, "ip:"+ip))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("machine at %s: %w", ip, ErrNotFound)
	}
	return m, err
}

// ListNodes returns every machine, or only a cluster's when cluster is non-empty.
func (s *Store) ListNodes(ctx context.Context, cluster string) ([]Machine, error) {
	q := `SELECT ` + machineCols + ` FROM machines`
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
	var out []Machine
	for rows.Next() {
		m, err := scanMachine(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

func (s *Store) macFor(ctx context.Context, ip string) (string, error) {
	m, err := s.GetNode(ctx, ip)
	if err != nil {
		return "", err
	}
	return m.MAC, nil
}

func (s *Store) SetNodeState(ctx context.Context, ip, state string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE machines SET state = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE ip = ? OR mac = ?`, state, ip, "ip:"+ip)
	return s.done(err, Change{Table: "machines", Key: ip, Op: "put"})
}

func (s *Store) AssignNode(ctx context.Context, ip, cluster, hostname, role string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE machines SET cluster = ?, hostname = ?, role = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE ip = ? OR mac = ?`, cluster, hostname, role, ip, "ip:"+ip)
	return s.done(err, Change{Table: "machines", Cluster: cluster, Key: ip, Op: "put"})
}

// UnassignNode detaches a machine from its cluster after a reset, keeping the inventory.
func (s *Store) UnassignNode(ctx context.Context, ip, state string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE machines SET cluster = NULL, hostname = '', pool = '', role = '', state = ?, machine_config = NULL, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE ip = ? OR mac = ?`, state, ip, "ip:"+ip)
	return s.done(err, Change{Table: "machines", Key: ip, Op: "put"})
}

func (s *Store) PutNodeMachineConfig(ctx context.Context, ip string, cfg []byte) error {
	sealed, err := s.crypto.Seal(cfg)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE machines SET machine_config = ? WHERE ip = ? OR mac = ?`, sealed, ip, "ip:"+ip)
	return err
}

func (s *Store) GetNodeMachineConfig(ctx context.Context, ip string) ([]byte, error) {
	var sealed []byte
	err := s.db.QueryRowContext(ctx, `SELECT machine_config FROM machines WHERE ip = ? OR mac = ?`, ip, "ip:"+ip).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && sealed == nil) {
		return nil, fmt.Errorf("machine config for %s: %w", ip, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	return s.crypto.Open(sealed)
}

// SetMachineOOB stores the out-of-band config with the password sealed; nil clears it.
func (s *Store) SetMachineOOB(ctx context.Context, mac string, c *oob.Config) error {
	raw := ""
	if c != nil && c.Type != "" {
		cp := *c
		sealed, err := s.seal(cp.Password)
		if err != nil {
			return err
		}
		cp.Password = sealed
		b, err := json.Marshal(cp)
		if err != nil {
			return err
		}
		raw = string(b)
	}
	_, err := s.db.ExecContext(ctx, `UPDATE machines SET oob = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE mac = ?`, raw, strings.ToLower(mac))
	return s.done(err, Change{Table: "machines", Key: strings.ToLower(mac), Op: "put"})
}

// MachineOOB returns the config with the password unsealed, for the backend only.
func (s *Store) MachineOOB(ctx context.Context, mac string) (*oob.Config, error) {
	m, err := s.GetMachine(ctx, mac)
	if err != nil {
		return nil, err
	}
	if m.OOB == nil {
		return nil, fmt.Errorf("machine %s: %w", mac, ErrNotFound)
	}
	c := *m.OOB
	c.Password = s.unseal(c.Password)
	return &c, nil
}

// LabHost is the state of a machine Kubit runs as a KVM host.
type LabHost struct {
	State     string           `json:"state"` // installing | setup | ready | error
	Error     string           `json:"error,omitempty"`
	Capacity  labhost.Capacity `json:"capacity"`
	Talos     string           `json:"talos,omitempty"`     // version of the boot assets on the host
	Schematic string           `json:"schematic,omitempty"` // schematic id of those assets
	Kernel    string           `json:"kernel,omitempty"`
	Initrd    string           `json:"initrd,omitempty"`
	Index     int              `json:"index"` // for MAC assignment
	VMs       []labhost.VM     `json:"vms"`
	Metrics   *labhost.Metrics `json:"metrics,omitempty"`
	Updates   *labhost.Updates `json:"updates,omitempty"`
	// Install is the installer's last reported stage while State is installing.
	Install *InstallProgress `json:"install,omitempty"`
	// Network is how VMs reach the LAN: bridge (default) or routed.
	Network string `json:"network,omitempty"`
	// Boot is what a manually booted install needs to be started with.
	Boot *BootLine `json:"boot,omitempty"`
	// Failures counts consecutive SSH failures; the watcher alerts on the third.
	Failures  int    `json:"failures,omitempty"`
	UpdatedAt string `json:"updatedAt"`
}

// BootLine is the installer kernel, initrd and command line for a manual boot.
type BootLine struct {
	Kernel  string `json:"kernel"`
	Initrd  string `json:"initrd"`
	Cmdline string `json:"cmdline"`
}

// InstallProgress is what the Debian installer last told Kubit.
type InstallProgress struct {
	Stage string `json:"stage"` // installer | partitioning | packages | late-done | booted
	At    string `json:"at"`
}

// SetLabHost stores the lab-host state (nil clears the role).
func (s *Store) SetLabHost(ctx context.Context, mac string, l *LabHost) error {
	raw := ""
	if l != nil {
		if l.VMs == nil {
			l.VMs = []labhost.VM{}
		}
		l.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		b, err := json.Marshal(l)
		if err != nil {
			return err
		}
		raw = string(b)
	}
	_, err := s.db.ExecContext(ctx, `UPDATE machines SET labhost = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE mac = ?`, raw, strings.ToLower(mac))
	return s.done(err, Change{Table: "machines", Key: strings.ToLower(mac), Op: "put"})
}

// SetMachineHost records which lab host a VM lives on.
func (s *Store) SetMachineHost(ctx context.Context, mac, host string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE machines SET host = ? WHERE mac = ?`, strings.ToLower(host), strings.ToLower(mac))
	return s.done(err, Change{Table: "machines", Key: strings.ToLower(mac), Op: "put"})
}

// NextLabHostIndex hands out the per-host byte used in VM MAC addresses.
func (s *Store) NextLabHostIndex(ctx context.Context) int {
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM machines WHERE labhost != ''`).Scan(&n)
	return n + 1
}

// SSHKey returns Kubit's key pair for lab hosts, minting it on first use. The private
// key is sealed in the settings table.
func (s *Store) SSHKey(ctx context.Context) (priv []byte, pub string, err error) {
	if sealed := s.GetValue(ctx, "ssh.priv"); sealed != "" {
		return []byte(s.unseal(sealed)), s.GetValue(ctx, "ssh.pub"), nil
	}
	priv, pub, err = labhost.GenerateKey()
	if err != nil {
		return nil, "", err
	}
	sealed, err := s.seal(string(priv))
	if err != nil {
		return nil, "", err
	}
	if err := s.SetValue(ctx, "ssh.priv", sealed); err != nil {
		return nil, "", err
	}
	return priv, pub, s.SetValue(ctx, "ssh.pub", pub)
}

// SetMachineWOL flags whether Kubit may send Wake-on-LAN packets to a machine.
func (s *Store) SetMachineWOL(ctx context.Context, mac string, on bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE machines SET wol = ? WHERE mac = ?`, b2i(on), strings.ToLower(mac))
	return s.done(err, Change{Table: "machines", Key: strings.ToLower(mac), Op: "put"})
}

// DeleteMachine forgets a machine that will not come back.
func (s *Store) DeleteMachine(ctx context.Context, mac string) error {
	_ = s.DeleteLabHostHistory(ctx, mac)
	_, err := s.db.ExecContext(ctx, `DELETE FROM machines WHERE mac = ?`, strings.ToLower(mac))
	return s.done(err, Change{Table: "machines", Key: strings.ToLower(mac), Op: "delete"})
}

// DeleteLabHostHistory drops the samples and events filed under a lab host's
// pseudo-cluster; called when the role is released or the machine retired.
func (s *Store) DeleteLabHostHistory(ctx context.Context, mac string) error {
	key := LabHostKey(mac)
	if _, err := s.db.ExecContext(ctx, `DELETE FROM samples WHERE cluster = ?`, key); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM events WHERE cluster = ?`, key)
	return err
}

// DeleteNode forgets the machine at an IP.
func (s *Store) DeleteNode(ctx context.Context, ip string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM machines WHERE ip = ? OR mac = ?`, ip, "ip:"+ip)
	return s.done(err, Change{Table: "machines", Key: ip, Op: "delete"})
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
