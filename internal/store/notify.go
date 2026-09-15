package store

import (
	"sync"
	"sync/atomic"
)

// Change is what a subscriber learns about a write: which table, which cluster it
// concerns ("" for Kubit-wide rows), the row key, and whether it was put or deleted.
// Subscribers fetch the row themselves; the store never marshals for them.
type Change struct {
	Table   string // clusters | machines | snapshots | operations | audit | events | settings | * (external writer)
	Cluster string
	Key     string // cluster name, machine mac, snapshot/operation/audit id, event kind…
	Op      string // put | delete | ack | resolve
	Node    string // events: the object the ack/resolve applies to
}

type notifier struct {
	mu     sync.RWMutex
	fns    []func(Change)
	writes atomic.Int64 // local write counter; external-writer detection compares against it
}

// OnChange registers a subscriber; fn must not block (it is called inline after the
// write commits).
func (s *Store) OnChange(fn func(Change)) {
	s.n.mu.Lock()
	defer s.n.mu.Unlock()
	s.n.fns = append(s.n.fns, fn)
}

func (s *Store) notify(c Change) {
	s.n.writes.Add(1)
	s.n.mu.RLock()
	fns := s.n.fns
	s.n.mu.RUnlock()
	for _, fn := range fns {
		fn(c)
	}
}

// done notifies when err is nil and passes err through, for the one-liner writers.
func (s *Store) done(err error, c Change) error {
	if err == nil {
		s.notify(c)
	}
	return err
}
