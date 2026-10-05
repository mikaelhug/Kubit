package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	_ "modernc.org/sqlite"
)

type Store struct {
	db     *sql.DB
	crypto *Crypto
	n      notifier

	settings settingsCache
}

func (s *Store) Crypto() *Crypto { return s.crypto }

func Open(dir string, crypto *Crypto) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "kubit.db")
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, crypto: crypto}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

var ErrCheckpointBusy = errors.New("checkpoint incomplete: the database is busy")

func (s *Store) Checkpoint(ctx context.Context) error {
	var busy, frames, done int
	if err := s.db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &frames, &done); err != nil {
		return err
	}
	if busy != 0 {
		return ErrCheckpointBusy
	}
	return nil
}

type scanner interface{ Scan(...any) error }

func queryAll[T any](ctx context.Context, db *sql.DB, scan func(scanner) (*T, error), query string, args ...any) ([]T, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

const sqlNow = `strftime('%Y-%m-%dT%H:%M:%fZ','now')`

var migrations = []string{
	`CREATE TABLE clusters (
		name          TEXT PRIMARY KEY,
		spec          TEXT NOT NULL,
		schematic_id  TEXT NOT NULL DEFAULT '',
		state         TEXT NOT NULL DEFAULT 'declared',
		created_at    TEXT NOT NULL DEFAULT (` + sqlNow + `),
		updated_at    TEXT NOT NULL DEFAULT (` + sqlNow + `)
	);
	CREATE TABLE cluster_secrets (
		cluster        TEXT PRIMARY KEY REFERENCES clusters(name) ON DELETE CASCADE,
		secrets_bundle BLOB NOT NULL,
		talosconfig    BLOB NOT NULL,
		kubeconfig     BLOB
	);
	CREATE TABLE nodes (
		ip           TEXT PRIMARY KEY,
		cluster      TEXT REFERENCES clusters(name) ON DELETE SET NULL,
		hostname     TEXT NOT NULL DEFAULT '',
		mac          TEXT NOT NULL DEFAULT '',
		arch         TEXT NOT NULL DEFAULT '',
		role         TEXT NOT NULL DEFAULT '',
		source       TEXT NOT NULL DEFAULT 'scan',
		state        TEXT NOT NULL DEFAULT 'discovered',
		hardware     TEXT NOT NULL DEFAULT '{}',
		talos_version TEXT NOT NULL DEFAULT '',
		machine_config BLOB,
		last_seen    TEXT,
		updated_at   TEXT NOT NULL DEFAULT (` + sqlNow + `)
	);
	CREATE TABLE operations (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		cluster     TEXT NOT NULL DEFAULT '',
		kind        TEXT NOT NULL,
		status      TEXT NOT NULL DEFAULT 'running',
		log         TEXT NOT NULL DEFAULT '',
		started_at  TEXT NOT NULL DEFAULT (` + sqlNow + `),
		finished_at TEXT
	);
	CREATE TABLE audit_log (
		id      INTEGER PRIMARY KEY AUTOINCREMENT,
		at      TEXT NOT NULL DEFAULT (` + sqlNow + `),
		cluster TEXT NOT NULL DEFAULT '',
		action  TEXT NOT NULL,
		detail  TEXT NOT NULL DEFAULT ''
	);`,
	`ALTER TABLE clusters ADD COLUMN platform TEXT NOT NULL DEFAULT '{}';`,
	`ALTER TABLE operations ADD COLUMN steps TEXT NOT NULL DEFAULT '[]';
	 ALTER TABLE operations ADD COLUMN artifact TEXT NOT NULL DEFAULT '';
	 ALTER TABLE operations ADD COLUMN request TEXT NOT NULL DEFAULT '';`,
	`CREATE TABLE samples (
		ts        TEXT NOT NULL,
		cluster   TEXT NOT NULL,
		node      TEXT NOT NULL DEFAULT '',
		cpu_milli INTEGER NOT NULL DEFAULT 0,
		cpu_cap   INTEGER NOT NULL DEFAULT 0,
		mem       INTEGER NOT NULL DEFAULT 0,
		mem_cap   INTEGER NOT NULL DEFAULT 0,
		pods      INTEGER NOT NULL DEFAULT 0,
		ready     INTEGER NOT NULL DEFAULT 0,
		reachable INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX samples_cluster_ts ON samples (cluster, node, ts);
	CREATE TABLE events (
		id       INTEGER PRIMARY KEY AUTOINCREMENT,
		ts       TEXT NOT NULL DEFAULT (` + sqlNow + `),
		cluster  TEXT NOT NULL,
		node     TEXT NOT NULL DEFAULT '',
		severity TEXT NOT NULL,
		kind     TEXT NOT NULL,
		message  TEXT NOT NULL,
		acked    INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX events_cluster_ts ON events (cluster, ts);`,
	`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);`,
	`CREATE TABLE machines (
		mac            TEXT PRIMARY KEY,
		uuid           TEXT NOT NULL DEFAULT '',
		serial         TEXT NOT NULL DEFAULT '',
		ip             TEXT UNIQUE,
		ips_seen       TEXT NOT NULL DEFAULT '[]',
		cluster        TEXT REFERENCES clusters(name) ON DELETE SET NULL,
		hostname       TEXT NOT NULL DEFAULT '',
		pool           TEXT NOT NULL DEFAULT '',
		role           TEXT NOT NULL DEFAULT '',
		arch           TEXT NOT NULL DEFAULT '',
		source         TEXT NOT NULL DEFAULT 'scan',
		state          TEXT NOT NULL DEFAULT 'discovered',
		hardware       TEXT NOT NULL DEFAULT '{}',
		talos_version  TEXT NOT NULL DEFAULT '',
		machine_config BLOB,
		wol            INTEGER NOT NULL DEFAULT 0,
		first_seen     TEXT NOT NULL DEFAULT (` + sqlNow + `),
		last_seen      TEXT,
		updated_at     TEXT NOT NULL DEFAULT (` + sqlNow + `)
	);
	INSERT INTO machines (mac, ip, cluster, hostname, role, arch, source, state, hardware, talos_version, machine_config, last_seen, updated_at)
		SELECT CASE WHEN mac = '' THEN 'ip:' || ip ELSE lower(mac) END, ip, cluster, hostname, role, arch, source, state, hardware, talos_version, machine_config, last_seen, updated_at
		FROM nodes WHERE 1 ORDER BY updated_at;
	DROP TABLE nodes;`,
	`CREATE TABLE snapshots (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		cluster       TEXT NOT NULL REFERENCES clusters(name) ON DELETE CASCADE,
		ts            TEXT NOT NULL DEFAULT (` + sqlNow + `),
		node          TEXT NOT NULL,
		path          TEXT NOT NULL,
		size_bytes    INTEGER NOT NULL,
		sha256        TEXT NOT NULL,
		keys          INTEGER NOT NULL DEFAULT 0,
		talos_version TEXT NOT NULL DEFAULT '',
		k8s_version   TEXT NOT NULL DEFAULT '',
		source        TEXT NOT NULL DEFAULT 'manual',
		status        TEXT NOT NULL DEFAULT 'ok'
	);
	CREATE INDEX snapshots_cluster_ts ON snapshots (cluster, ts);`,
	`ALTER TABLE snapshots ADD COLUMN offsite TEXT NOT NULL DEFAULT '';`,
	`ALTER TABLE machines ADD COLUMN oob TEXT NOT NULL DEFAULT '';
	 ALTER TABLE machines ADD COLUMN provision INTEGER NOT NULL DEFAULT 0;`,
	`ALTER TABLE machines ADD COLUMN labhost TEXT NOT NULL DEFAULT '';
	 ALTER TABLE machines ADD COLUMN host TEXT NOT NULL DEFAULT '';
	 ALTER TABLE machines ADD COLUMN provision_kind TEXT NOT NULL DEFAULT '';`,
	`ALTER TABLE samples ADD COLUMN disk INTEGER NOT NULL DEFAULT 0;
	 ALTER TABLE samples ADD COLUMN disk_cap INTEGER NOT NULL DEFAULT 0;`,
	`UPDATE machines SET state = 'unknown' WHERE state = 'configured' AND source = 'labhost';`,
	`CREATE TABLE users (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		name          TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL DEFAULT '',
		role          TEXT NOT NULL DEFAULT 'viewer',
		disabled      INTEGER NOT NULL DEFAULT 0,
		source        TEXT NOT NULL DEFAULT 'local',
		created_at    TEXT NOT NULL DEFAULT (` + sqlNow + `),
		last_login    TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE sessions (
		token_hash TEXT PRIMARY KEY,
		user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		kind       TEXT NOT NULL DEFAULT 'session',
		name       TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT (` + sqlNow + `),
		expires_at TEXT NOT NULL DEFAULT '',
		last_used  TEXT NOT NULL DEFAULT ''
	);
	ALTER TABLE audit_log ADD COLUMN actor TEXT NOT NULL DEFAULT '';`,
	`CREATE TABLE sops_keys (
		cluster    TEXT PRIMARY KEY,
		identity   BLOB NOT NULL,
		recipient  TEXT NOT NULL,
		created_at TEXT NOT NULL DEFAULT (` + sqlNow + `)
	);`,
	`UPDATE clusters SET platform = json_remove(platform, '$.outputs.argocd_admin_password', '$.outputs.argocd_ip') WHERE json_valid(platform);`,
	`ALTER TABLE machines ADD COLUMN system_split INTEGER NOT NULL DEFAULT 0;`,
	`CREATE TABLE operation_log (
		op_id INTEGER NOT NULL REFERENCES operations(id) ON DELETE CASCADE,
		seq   INTEGER NOT NULL,
		line  TEXT NOT NULL,
		PRIMARY KEY (op_id, seq)
	);`,
	`CREATE INDEX IF NOT EXISTS events_open ON events (cluster, kind, node) WHERE acked = 0;
	 CREATE INDEX IF NOT EXISTS operations_kind ON operations (kind, cluster, id);`,
}

var alreadyApplied = map[int]string{
	13: `SELECT actor FROM audit_log LIMIT 0`,
	14: `SELECT cluster FROM sops_keys LIMIT 0`,
	16: `SELECT system_split FROM machines LIMIT 0`,
	17: `SELECT op_id FROM operation_log LIMIT 0`,
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return err
	}
	var current int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&current); err != nil {
		return err
	}
	for i := current; i < len(migrations); i++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if probe, ok := alreadyApplied[i+1]; ok {
			if _, err := tx.ExecContext(ctx, probe); err == nil {
				if _, err := tx.ExecContext(ctx, `INSERT INTO schema_version (version) VALUES (?)`, i+1); err != nil {
					tx.Rollback()
					return err
				}
				if err := tx.Commit(); err != nil {
					return err
				}
				continue
			}
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_version (version) VALUES (?)`, i+1); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Audit(ctx context.Context, cluster, action, detail string) error {
	res, err := s.db.ExecContext(ctx, `INSERT INTO audit_log (cluster, action, detail, actor) VALUES (?, ?, ?, ?)`, cluster, action, detail, "")
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	s.notify(Change{Table: "audit", Cluster: cluster, Key: strconv.FormatInt(id, 10), Op: "put"})
	return nil
}

type AuditEntry struct {
	ID      int64  `json:"id"`
	At      string `json:"at"`
	Cluster string `json:"cluster"`
	Action  string `json:"action"`
	Detail  string `json:"detail"`
	Actor   string `json:"actor,omitempty"`
}

const auditCols = `id, at, cluster, action, detail, actor`

func scanAudit(sc scanner) (*AuditEntry, error) {
	var e AuditEntry
	if err := sc.Scan(&e.ID, &e.At, &e.Cluster, &e.Action, &e.Detail, &e.Actor); err != nil {
		return nil, err
	}
	return &e, nil
}

func (s *Store) GetAudit(ctx context.Context, id int64) (*AuditEntry, error) {
	e, err := scanAudit(s.db.QueryRowContext(ctx, `SELECT `+auditCols+` FROM audit_log WHERE id = ?`, id))
	return e, notFound(err, "audit entry %d", id)
}

func (s *Store) ListAudit(ctx context.Context, cluster string, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	if cluster != "" {
		return queryAll(ctx, s.db, scanAudit, `SELECT `+auditCols+` FROM audit_log WHERE cluster = ? ORDER BY id DESC LIMIT ?`, cluster, limit)
	}
	return queryAll(ctx, s.db, scanAudit, `SELECT `+auditCols+` FROM audit_log ORDER BY id DESC LIMIT ?`, limit)
}
