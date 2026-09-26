package store

import (
	"sync"
	"sync/atomic"
)

type Change struct {
	Table   string
	Cluster string
	Key     string
	Op      string
	Node    string
}

type notifier struct {
	mu     sync.RWMutex
	fns    []func(Change)
	writes atomic.Int64
}

func (s *Store) OnChange(fn func(Change)) {
	s.n.mu.Lock()
	defer s.n.mu.Unlock()
	s.n.fns = append(s.n.fns, fn)
}

func (s *Store) notify(c Change) {
	if c.Table == "settings" || c.Table == "*" {
		s.settings.invalidate()
	}
	s.n.writes.Add(1)
	s.n.mu.RLock()
	fns := s.n.fns
	s.n.mu.RUnlock()
	for _, fn := range fns {
		fn(c)
	}
}

func (s *Store) done(err error, c Change) error {
	if err == nil {
		s.notify(c)
	}
	return err
}
