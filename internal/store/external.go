package store

import (
	"context"
	"database/sql"
	"time"
)

// WatchExternal reports writes made by other processes (the CLI while the daemon
// runs) by polling SQLite's data_version on a dedicated connection. That counter only
// moves when *another* connection commits; local writes are told apart by the
// notifier's own counter. The poll is daemon-side and cheap (one PRAGMA every 2 s), so
// consoles never have to.
func (s *Store) WatchExternal(ctx context.Context, interval time.Duration) {
	// The pool is capped at one connection, so the probe gets its own; that also
	// makes local writes look "external" to it, which the write counter filters out.
	conn, err := sql.Open("sqlite", s.dsn)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetMaxOpenConns(1)
	var last int64
	_ = conn.QueryRowContext(ctx, `PRAGMA data_version`).Scan(&last)
	lastWrites := s.n.writes.Load()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		var v int64
		if err := conn.QueryRowContext(ctx, `PRAGMA data_version`).Scan(&v); err != nil {
			continue
		}
		writes := s.n.writes.Load()
		if v != last && writes == lastWrites {
			s.notify(Change{Table: "*", Op: "put"})
		}
		last, lastWrites = v, writes
	}
}
