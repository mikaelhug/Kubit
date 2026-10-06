package store

import "sync"

type Change struct {
	Table   string
	Cluster string
	Key     string
}

type notifier struct {
	mu  sync.RWMutex
	fns []func(Change)
}

func (s *Store) OnChange(fn func(Change)) {
	s.n.mu.Lock()
	defer s.n.mu.Unlock()
	s.n.fns = append(s.n.fns, fn)
}

func (s *Store) notify(c Change) {
	s.n.mu.RLock()
	fns := s.n.fns
	s.n.mu.RUnlock()
	for _, fn := range fns {
		fn(c)
	}
}
