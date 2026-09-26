package main

import (
	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/store"
	"github.com/spf13/cobra"
)

func homeDir() (string, error) { return store.HomeDir() }

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
	s, err := openStore()
	if err != nil {
		return nil, nil, err
	}
	return cluster.NewManager(s, dir), s.Crypto(), nil
}

func withManager(fn func(cmd *cobra.Command, args []string, m *cluster.Manager) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		m, err := openManager()
		if err != nil {
			return err
		}
		defer m.Store.Close()
		return fn(cmd, args, m)
	}
}

func withStore(fn func(cmd *cobra.Command, args []string, s *store.Store) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		s, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close()
		return fn(cmd, args, s)
	}
}
