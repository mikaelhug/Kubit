package store

import (
	"context"
	"time"
)

func (s *Store) dataVersion(ctx context.Context) (int64, error) {
	var v int64
	err := s.db.QueryRowContext(ctx, `PRAGMA data_version`).Scan(&v)
	return v, err
}

func (s *Store) WatchExternal(ctx context.Context, interval time.Duration) {
	last, _ := s.dataVersion(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		v, err := s.dataVersion(ctx)
		if err != nil || v == last {
			continue
		}
		last = v
		s.notify(Change{Table: "*", Op: "put"})
	}
}
