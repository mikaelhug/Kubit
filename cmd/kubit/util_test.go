package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/store"
	"github.com/spf13/cobra"
)

func TestWithManagerOpensAndClosesTheStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KUBIT_HOME", home)
	t.Setenv(store.EnvMasterKey, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{5}, 32)))
	var kept *cluster.Manager
	boom := errors.New("boom")
	err := withManager(func(cmd *cobra.Command, args []string, m *cluster.Manager) error {
		kept = m
		if m.Home != home || len(args) != 1 || args[0] != "x" {
			t.Errorf("home %q args %v", m.Home, args)
		}
		if _, err := m.Store.ListClusters(t.Context()); err != nil {
			t.Errorf("store not usable: %v", err)
		}
		return boom
	})(&cobra.Command{}, []string{"x"})
	if !errors.Is(err, boom) {
		t.Fatalf("the command's error must pass through: %v", err)
	}
	if _, err := kept.Store.ListClusters(t.Context()); err == nil {
		t.Error("the store must be closed after the command")
	}
}
