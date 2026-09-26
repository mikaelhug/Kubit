package store

import (
	"context"
	"database/sql"
	"time"
)

func (s *Store) WatchExternal(ctx context.Context, interval time.Duration) {
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
		if v != last {
			s.settings.invalidate()
			if writes == lastWrites {
				s.notify(Change{Table: "*", Op: "put"})
			}
		}
		last, lastWrites = v, writes
	}
}
