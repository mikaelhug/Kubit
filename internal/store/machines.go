package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/labhost"
	"github.com/mikael/kubit/internal/labhost/libvirt"
	"github.com/mikael/kubit/internal/netx"
	"github.com/mikael/kubit/internal/oob"
)

type Machine struct {
	MAC           string      `json:"mac"`
	UUID          string      `json:"uuid,omitempty"`
	Serial        string      `json:"serial,omitempty"`
	IP            string      `json:"ip"`
	IPsSeen       []string    `json:"ipsSeen,omitempty"`
	Cluster       string      `json:"cluster"`
	Hostname      string      `json:"hostname"`
	Pool          string      `json:"pool"`
	Role          string      `json:"role"`
	Arch          string      `json:"arch"`
	Source        string      `json:"source"`
	State         string      `json:"state"`
	Hardware      []byte      `json:"-"`
	TalosVersion  string      `json:"talosVersion"`
	WOL           bool        `json:"wol"`
	OOB           *oob.Config `json:"oob,omitempty"`
	OOBType       string      `json:"oobType,omitempty"`
	Provision     bool        `json:"provision"`
	ProvisionKind string      `json:"provisionKind,omitempty"`
	LabHost       *LabHost    `json:"labhost,omitempty"`
	Host          string      `json:"host,omitempty"`
	FirstSeen     string      `json:"firstSeen"`
	LastSeen      string      `json:"lastSeen"`
	UpdatedAt     string      `json:"updatedAt"`
}

type NodeRow = Machine

type Kind string

const (
	KindMember      Kind = "member"
	KindMaintenance Kind = "maintenance"
	KindConfigured  Kind = "configured"
	KindLabHost     Kind = "labhost"
	KindBooting     Kind = "booting"
	KindUnbooted    Kind = "unbooted"
)

func (m *Machine) Kind() Kind {
	switch {
	case m.Cluster != "":
		return KindMember
	case m.LabHost != nil:
		return KindLabHost
	case m.State == "maintenance":
		return KindMaintenance
	case m.State == "configured":
		return KindConfigured
	case m.Provision && m.ProvisionKind == "talos", m.State == "booting", m.State == "installing":
		return KindBooting
	default:
		return KindUnbooted
	}
}

func (m *Machine) Talos() bool {
	k := m.Kind()
	return k == KindMember || k == KindMaintenance
}

func (m *Machine) IsLabVM() bool { return m.Host != "" }

const byIPOrMAC = ` WHERE ip = ? OR mac = ?`

func ipArgs(ip string, set ...any) []any {
	return append(set, ip, "ip:"+ip)
}

func MachineKey(mac, ip string) string {
	if mac != "" {
		return netx.MACKey(mac)
	}
	return "ip:" + ip
}

const machineCols = `mac, uuid, serial, COALESCE(ip,''), ips_seen, COALESCE(cluster,''), hostname, pool, role, arch, source, state, hardware, talos_version, wol, first_seen, COALESCE(last_seen,''), updated_at, oob, provision, labhost, host, provision_kind`

func scanMachine(sc scanner) (*Machine, error) {
	var m Machine
	var hw, seen, oobRaw, lab string
	var wol, prov int
	if err := sc.Scan(&m.MAC, &m.UUID, &m.Serial, &m.IP, &seen, &m.Cluster, &m.Hostname, &m.Pool, &m.Role, &m.Arch, &m.Source, &m.State, &hw, &m.TalosVersion, &wol, &m.FirstSeen, &m.LastSeen, &m.UpdatedAt, &oobRaw, &prov, &lab, &m.Host, &m.ProvisionKind); err != nil {
		return nil, err
	}
	m.Provision = prov == 1
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

func (s *Store) UpsertNode(ctx context.Context, n Machine) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	key := MachineKey(n.MAC, n.IP)
	if n.MAC == "" && n.IP != "" {
		if existing, err := machineByIP(ctx, tx, n.IP); err == nil {
			key = existing.MAC
		}
	}
	var moved int64
	if n.IP != "" {
		res, err := tx.ExecContext(ctx, `UPDATE machines SET ip = NULL WHERE ip = ? AND mac != ?`, n.IP, key)
		if err != nil {
			return err
		}
		moved, _ = res.RowsAffected()
	}
	before, seen := machineFingerprint(ctx, tx, key)
	hw := string(n.Hardware)
	if hw == "" {
		hw = "{}"
	}
	prev, _ := machineByMAC(ctx, tx, key)
	ips := []string{}
	if prev != nil {
		ips = prev.IPsSeen
		if prev.IP != "" && prev.IP != n.IP && !slices.Contains(ips, prev.IP) {
			ips = append(ips, prev.IP)
		}
	}
	ipsJSON, _ := json.Marshal(ips)
	var cluster any
	if n.Cluster != "" {
		cluster = n.Cluster
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO machines (mac, uuid, serial, ip, ips_seen, cluster, hostname, pool, role, arch, source, state, hardware, talos_version, last_seen)
		VALUES (?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, `+sqlNow+`)
		ON CONFLICT(mac) DO UPDATE SET
			uuid          = CASE WHEN excluded.uuid = '' THEN machines.uuid ELSE excluded.uuid END,
			serial        = CASE WHEN excluded.serial = '' THEN machines.serial ELSE excluded.serial END,
			ip            = COALESCE(excluded.ip, machines.ip),
			ips_seen      = excluded.ips_seen,
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
			provision     = CASE WHEN excluded.state = 'maintenance' THEN 0 ELSE machines.provision END,
			system_split  = CASE WHEN excluded.state = 'maintenance' AND excluded.cluster IS NULL THEN 0 ELSE machines.system_split END,
			updated_at    = `+sqlNow,
		key, n.UUID, n.Serial, n.IP, string(ipsJSON), cluster, n.Hostname, n.Pool, n.Role, n.Arch, n.Source, n.State, hw, n.TalosVersion); err != nil {
		return err
	}
	if after, _ := machineFingerprint(ctx, tx, key); moved == 0 && before != "" && before == after && seenWithin(seen, lastSeenEvery) {
		return nil
	}
	return s.done(tx.Commit(), Change{Table: "machines", Cluster: n.Cluster, Key: key, Op: "put"})
}

const lastSeenEvery = 30 * time.Second

func machineFingerprint(ctx context.Context, q rowQuerier, mac string) (fingerprint, lastSeen string) {
	_ = q.QueryRowContext(ctx, `SELECT json_array(uuid, serial, ip, ips_seen, cluster, hostname, pool, role, arch, source, state, hardware, talos_version, provision, system_split), COALESCE(last_seen, '') FROM machines WHERE mac = ?`, mac).Scan(&fingerprint, &lastSeen)
	return fingerprint, lastSeen
}

func seenWithin(ts string, d time.Duration) bool {
	t, err := time.Parse(time.RFC3339Nano, ts)
	return err == nil && time.Since(t) < d
}

type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (s *Store) GetMachine(ctx context.Context, mac string) (*Machine, error) {
	return machineByMAC(ctx, s.db, mac)
}

func (s *Store) MachineIPs(ctx context.Context, macs []string) (map[string]string, error) {
	out := map[string]string{}
	if len(macs) == 0 {
		return out, nil
	}
	args := make([]any, len(macs))
	for i, mac := range macs {
		args[i] = netx.MACKey(mac)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT mac, COALESCE(ip,'') FROM machines WHERE mac IN (?`+strings.Repeat(`, ?`, len(macs)-1)+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var mac, ip string
		if err := rows.Scan(&mac, &ip); err != nil {
			return nil, err
		}
		out[mac] = ip
	}
	return out, rows.Err()
}

func machineByMAC(ctx context.Context, q rowQuerier, mac string) (*Machine, error) {
	m, err := scanMachine(q.QueryRowContext(ctx, `SELECT `+machineCols+` FROM machines WHERE mac = ?`, netx.MACKey(mac)))
	return m, notFound(err, "machine %s", mac)
}

func (s *Store) GetNode(ctx context.Context, ip string) (*Machine, error) {
	return machineByIP(ctx, s.db, ip)
}

func machineByIP(ctx context.Context, q rowQuerier, ip string) (*Machine, error) {
	m, err := scanMachine(q.QueryRowContext(ctx, `SELECT `+machineCols+` FROM machines`+byIPOrMAC, ipArgs(ip)...))
	return m, notFound(err, "machine at %s", ip)
}

func (s *Store) ListNodes(ctx context.Context, cluster string) ([]Machine, error) {
	q := `SELECT ` + machineCols + ` FROM machines`
	var args []any
	if cluster != "" {
		q += ` WHERE cluster = ?`
		args = append(args, cluster)
	}
	return queryAll(ctx, s.db, scanMachine, q+` ORDER BY cluster, role, hostname, ip`, args...)
}

func (s *Store) SetNodeState(ctx context.Context, ip, state string) error {
	return s.updateAt(ctx, ip, "", `state = ?`, state)
}

func (s *Store) AssignNode(ctx context.Context, ip, cluster, hostname, role string) error {
	return s.updateAt(ctx, ip, cluster, `cluster = ?, hostname = ?, role = ?`, cluster, hostname, role)
}

func (s *Store) UnassignNode(ctx context.Context, ip, state string) error {
	return s.updateAt(ctx, ip, "", `cluster = NULL, hostname = '', pool = '', role = '', state = ?, machine_config = NULL, system_split = 0`, state)
}

func (s *Store) UnassignMachine(ctx context.Context, mac, ip, state string) error {
	if mac == "" {
		return s.UnassignNode(ctx, ip, state)
	}
	key := netx.MACKey(mac)
	res, err := s.db.ExecContext(ctx, `UPDATE machines SET cluster = NULL, hostname = '', pool = '', role = '', state = ?, machine_config = NULL, system_split = 0, updated_at = `+sqlNow+` WHERE mac = ?`, state, key)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return s.UnassignNode(ctx, ip, state)
	}
	s.notify(Change{Table: "machines", Key: key, Op: "put"})
	return nil
}

func (s *Store) updateAt(ctx context.Context, ip, cluster, set string, args ...any) error {
	macs, err := queryAll(ctx, s.db, func(sc scanner) (*string, error) {
		var mac string
		return &mac, sc.Scan(&mac)
	}, `UPDATE machines SET `+set+`, updated_at = `+sqlNow+byIPOrMAC+` RETURNING mac`, ipArgs(ip, args...)...)
	if err != nil {
		return err
	}
	for _, mac := range macs {
		s.notify(Change{Table: "machines", Cluster: cluster, Key: mac, Op: "put"})
	}
	return nil
}

func (s *Store) PutNodeMachineConfig(ctx context.Context, ip string, cfg []byte, systemSplit bool) error {
	sealed, err := s.crypto.Seal(cfg)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE machines SET machine_config = ?, system_split = MAX(system_split, ?)`+byIPOrMAC, ipArgs(ip, sealed, systemSplit)...)
	return err
}

func (s *Store) NodeSystemSplit(ctx context.Context, ip string) bool {
	var split bool
	_ = s.db.QueryRowContext(ctx, `SELECT system_split FROM machines`+byIPOrMAC, ipArgs(ip)...).Scan(&split)
	return split
}

func (s *Store) GetNodeMachineConfig(ctx context.Context, ip string) ([]byte, error) {
	cfg, _, err := s.NodeMachineConfigSplit(ctx, ip)
	return cfg, err
}

func (s *Store) NodeMachineConfigSplit(ctx context.Context, ip string) ([]byte, bool, error) {
	var sealed []byte
	var split bool
	err := s.db.QueryRowContext(ctx, `SELECT machine_config, system_split FROM machines`+byIPOrMAC, ipArgs(ip)...).Scan(&sealed, &split)
	if err == nil && sealed == nil {
		err = sql.ErrNoRows
	}
	if err := notFound(err, "machine config for %s", ip); err != nil {
		return nil, false, err
	}
	cfg, err := s.crypto.Open(sealed)
	return cfg, split, err
}

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
	_, err := s.db.ExecContext(ctx, `UPDATE machines SET oob = ?, updated_at = `+sqlNow+` WHERE mac = ?`, raw, netx.MACKey(mac))
	return s.done(err, Change{Table: "machines", Key: netx.MACKey(mac), Op: "put"})
}

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

func (s *Store) SetMachineProvision(ctx context.Context, mac string, on bool, kind ...string) error {
	k := ""
	if on && len(kind) > 0 {
		k = kind[0]
	} else if on {
		k = "talos"
	}
	_, err := s.db.ExecContext(ctx, `UPDATE machines SET provision = ?, provision_kind = ? WHERE mac = ?`, b2i(on), k, netx.MACKey(mac))
	return s.done(err, Change{Table: "machines", Key: netx.MACKey(mac), Op: "put"})
}

type LabHost struct {
	State     string           `json:"state"`
	Error     string           `json:"error,omitempty"`
	Capacity  labhost.Capacity `json:"capacity"`
	Talos     string           `json:"talos,omitempty"`
	Schematic string           `json:"schematic,omitempty"`
	Kernel    string           `json:"kernel,omitempty"`
	Initrd    string           `json:"initrd,omitempty"`
	ISO       string           `json:"iso,omitempty"`
	Driver    string           `json:"driver,omitempty"`
	Index     int              `json:"index"`
	VMs       []labhost.VM     `json:"vms"`
	Metrics   *labhost.Metrics `json:"metrics,omitempty"`
	Updates   *labhost.Updates `json:"updates,omitempty"`
	Install   *InstallProgress `json:"install,omitempty"`
	Network   string           `json:"network,omitempty"`
	Disk      string           `json:"disk,omitempty"`
	Boot      *BootLine        `json:"boot,omitempty"`
	Failures  int              `json:"failures,omitempty"`
	UpdatedAt string           `json:"updatedAt"`
}

type BootLine struct {
	Kernel  string `json:"kernel"`
	Initrd  string `json:"initrd"`
	Cmdline string `json:"cmdline"`
}

type InstallProgress struct {
	Stage string `json:"stage"`
	At    string `json:"at"`
}

func (s *Store) SetLabHost(ctx context.Context, mac string, l *LabHost) error {
	lk := s.labLock(netx.MACKey(mac))
	lk.Lock()
	defer lk.Unlock()
	return s.setLabHostLocked(ctx, mac, l)
}

func (s *Store) UpdateLabHost(ctx context.Context, mac string, mutate func(*LabHost)) error {
	mac = netx.MACKey(mac)
	lk := s.labLock(mac)
	lk.Lock()
	defer lk.Unlock()
	m, err := s.GetMachine(ctx, mac)
	if err != nil {
		return err
	}
	if m.LabHost == nil {
		return nil
	}
	mutate(m.LabHost)
	return s.setLabHostLocked(ctx, mac, m.LabHost)
}

func (s *Store) setLabHostLocked(ctx context.Context, mac string, l *LabHost) error {
	mac = netx.MACKey(mac)
	if l == nil {
		_, err := s.db.ExecContext(ctx, `UPDATE machines SET labhost = '', updated_at = `+sqlNow+` WHERE mac = ?`, mac)
		return s.done(err, Change{Table: "machines", Key: mac, Op: "put"})
	}
	if l.VMs == nil {
		l.VMs = []labhost.VM{}
	}
	l.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	b, err := json.Marshal(l)
	if err != nil {
		return err
	}
	if l.Index > 0 {
		_, err = s.db.ExecContext(ctx, `UPDATE machines SET labhost = ?, updated_at = `+sqlNow+` WHERE mac = ?`, string(b), mac)
		return s.done(err, Change{Table: "machines", Key: mac, Op: "put"})
	}
	var index int
	err = s.db.QueryRowContext(ctx, `UPDATE machines SET labhost = json_set(?, '$.index', (`+nextLabIndex+`)), updated_at = `+sqlNow+` WHERE mac = ? RETURNING CAST(json_extract(labhost, '$.index') AS INTEGER)`, string(b), mac, mac).Scan(&index)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err == nil {
		l.Index = index
	}
	return s.done(err, Change{Table: "machines", Key: mac, Op: "put"})
}

const nextLabIndex = `SELECT COALESCE(MAX(CAST(json_extract(labhost, '$.index') AS INTEGER)), 0) + 1 FROM machines WHERE labhost != '' AND mac != ?`

func (s *Store) SetMachineHost(ctx context.Context, mac, host string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE machines SET host = ? WHERE mac = ?`, netx.MACKey(host), netx.MACKey(mac))
	return s.done(err, Change{Table: "machines", Key: netx.MACKey(mac), Op: "put"})
}

func (s *Store) SSHKey(ctx context.Context) (priv []byte, pub string, err error) {
	if sealed := s.GetValue(ctx, "ssh.priv"); sealed != "" {
		return []byte(s.unseal(sealed)), s.GetValue(ctx, "ssh.pub"), nil
	}
	newPriv, newPub, err := libvirt.GenerateKey()
	if err != nil {
		return nil, "", err
	}
	sealed, err := s.seal(string(newPriv))
	if err != nil {
		return nil, "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback()
	var stored string
	switch err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'ssh.priv'`).Scan(&stored); {
	case err == nil && stored != "":
		if err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'ssh.pub'`).Scan(&pub); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, "", err
		}
		return []byte(s.unseal(stored)), pub, nil
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return nil, "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('ssh.priv', ?), ('ssh.pub', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, sealed, newPub); err != nil {
		return nil, "", err
	}
	if err := tx.Commit(); err != nil {
		return nil, "", err
	}
	s.notify(Change{Table: "settings", Key: "ssh.pub", Op: "put"})
	return newPriv, newPub, nil
}

func (s *Store) SetMachineWOL(ctx context.Context, mac string, on bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE machines SET wol = ? WHERE mac = ?`, b2i(on), netx.MACKey(mac))
	return s.done(err, Change{Table: "machines", Key: netx.MACKey(mac), Op: "put"})
}

func (s *Store) DeleteMachine(ctx context.Context, mac string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := deleteLabHostHistory(ctx, tx, mac); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM machines WHERE mac = ?`, netx.MACKey(mac)); err != nil {
		return err
	}
	return s.done(tx.Commit(), Change{Table: "machines", Key: netx.MACKey(mac), Op: "delete"})
}

func (s *Store) DeleteLabHostHistory(ctx context.Context, mac string) error {
	return deleteLabHostHistory(ctx, s.db, mac)
}

func deleteLabHostHistory(ctx context.Context, x execer, mac string) error {
	key := LabHostKey(mac)
	if _, err := x.ExecContext(ctx, `DELETE FROM samples WHERE cluster = ?`, key); err != nil {
		return err
	}
	_, err := x.ExecContext(ctx, `DELETE FROM events WHERE cluster = ?`, key)
	return err
}
