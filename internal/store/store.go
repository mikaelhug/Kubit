// Package store is Kubit's SQLite-backed inventory: clusters, their encrypted secrets,
// nodes, operations and the audit log.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type Store struct {
	db     *sql.DB
	crypto *Crypto
}

// Open creates dir (0700) if needed, opens dir/kubit.db and applies migrations.
func Open(dir string, crypto *Crypto) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "kubit.db")
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, crypto: crypto}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	// The directory is 0700; this only tightens the file itself (created lazily above).
	if err := os.Chmod(path, 0o600); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Checkpoint folds the WAL into the main database file so a file-level copy is complete.
func (s *Store) Checkpoint(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}

var migrations = []string{
	`CREATE TABLE clusters (
		name          TEXT PRIMARY KEY,
		spec          TEXT NOT NULL,            -- cluster.yaml as stored
		schematic_id  TEXT NOT NULL DEFAULT '',
		state         TEXT NOT NULL DEFAULT 'declared',
		created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
		updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
	);
	CREATE TABLE cluster_secrets (
		cluster        TEXT PRIMARY KEY REFERENCES clusters(name) ON DELETE CASCADE,
		secrets_bundle BLOB NOT NULL,           -- sealed secrets.yaml
		talosconfig    BLOB NOT NULL,           -- sealed
		kubeconfig     BLOB                      -- sealed, set after bootstrap
	);
	CREATE TABLE nodes (
		ip           TEXT PRIMARY KEY,
		cluster      TEXT REFERENCES clusters(name) ON DELETE SET NULL,
		hostname     TEXT NOT NULL DEFAULT '',
		mac          TEXT NOT NULL DEFAULT '',
		arch         TEXT NOT NULL DEFAULT '',
		role         TEXT NOT NULL DEFAULT '',
		source       TEXT NOT NULL DEFAULT 'scan', -- scan | manual | pxe
		state        TEXT NOT NULL DEFAULT 'discovered',
		hardware     TEXT NOT NULL DEFAULT '{}',  -- JSON inventory from discovery
		talos_version TEXT NOT NULL DEFAULT '',
		machine_config BLOB,                     -- sealed, last applied
		last_seen    TEXT,
		updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
	);
	CREATE TABLE operations (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		cluster     TEXT NOT NULL DEFAULT '',
		kind        TEXT NOT NULL,
		status      TEXT NOT NULL DEFAULT 'running',
		log         TEXT NOT NULL DEFAULT '',
		started_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
		finished_at TEXT
	);
	CREATE TABLE audit_log (
		id      INTEGER PRIMARY KEY AUTOINCREMENT,
		at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
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
		node      TEXT NOT NULL DEFAULT '',   -- '' = cluster totals
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
		ts       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
		cluster  TEXT NOT NULL,
		node     TEXT NOT NULL DEFAULT '',
		severity TEXT NOT NULL,              -- info | warn | critical
		kind     TEXT NOT NULL,
		message  TEXT NOT NULL,
		acked    INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX events_cluster_ts ON events (cluster, ts);`,
	`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);`,
	`CREATE TABLE machines (
		mac            TEXT PRIMARY KEY,           -- uplink MAC, lower-case; "ip:<ip>" when unknown
		uuid           TEXT NOT NULL DEFAULT '',   -- SMBIOS system UUID
		serial         TEXT NOT NULL DEFAULT '',
		ip             TEXT UNIQUE,                -- where it is reachable now
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
		first_seen     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
		last_seen      TEXT,
		updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
	);
	INSERT INTO machines (mac, ip, cluster, hostname, role, arch, source, state, hardware, talos_version, machine_config, last_seen, updated_at)
		SELECT CASE WHEN mac = '' THEN 'ip:' || ip ELSE lower(mac) END, ip, cluster, hostname, role, arch, source, state, hardware, talos_version, machine_config, last_seen, updated_at
		FROM nodes WHERE 1 ORDER BY updated_at;
	DROP TABLE nodes;`,
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
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit_log (cluster, action, detail) VALUES (?, ?, ?)`, cluster, action, detail)
	return err
}
