package store

import (
	"errors"
	"fmt"
	"sync"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	mu       sync.Mutex
	clusters map[string]ClusterRow
	secrets  map[string]ClusterSecrets
	hashes   map[string]string
	machines map[string]Machine
	n        notifier
}

func New() *Store {
	return &Store{clusters: map[string]ClusterRow{}, secrets: map[string]ClusterSecrets{}, hashes: map[string]string{}, machines: map[string]Machine{}}
}

func notFound(format string, args ...any) error {
	return fmt.Errorf(format+": %w", append(args, ErrNotFound)...)
}
