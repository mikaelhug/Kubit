package main

import (
	"os"
	"path/filepath"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/store"
)

// homeDir is $KUBIT_HOME or ~/.kubit.
func homeDir() (string, error) {
	if h := os.Getenv("KUBIT_HOME"); h != "" {
		return h, nil
	}
	u, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(u, ".kubit"), nil
}

func openStore() (*store.Store, error) {
	dir, err := homeDir()
	if err != nil {
		return nil, err
	}
	crypto, err := store.LoadCrypto()
	if err != nil {
		return nil, err
	}
	return store.Open(dir, crypto)
}

func openManager() (*cluster.Manager, error) {
	m, _, err := openManagerCrypto()
	return m, err
}

func openManagerCrypto() (*cluster.Manager, *store.Crypto, error) {
	dir, err := homeDir()
	if err != nil {
		return nil, nil, err
	}
	crypto, err := store.LoadCrypto()
	if err != nil {
		return nil, nil, err
	}
	s, err := store.Open(dir, crypto)
	if err != nil {
		return nil, nil, err
	}
	return cluster.NewManager(s, dir), crypto, nil
}
