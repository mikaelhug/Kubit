package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mikaelhug/kubit/internal/cluster"
	"github.com/mikaelhug/kubit/internal/store"
	"github.com/spf13/cobra"
)

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

func openManager() (*cluster.Manager, error) {
	dir, err := homeDir()
	if err != nil {
		return nil, err
	}
	return cluster.NewManager(store.New(), dir), nil
}

func withManager(fn func(cmd *cobra.Command, args []string, m *cluster.Manager) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		m, err := openManager()
		if err != nil {
			return err
		}
		return fn(cmd, args, m)
	}
}

func printEvents(cmd *cobra.Command) cluster.Sink {
	return func(e cluster.Event) {
		switch e.Kind {
		case cluster.KindStep:
			if e.Status == cluster.StepRunning {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s ▶ %s\n", e.Time.Format("15:04:05"), e.Step)
			} else if e.Status == cluster.StepFailed {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s ✗ %s\n", e.Time.Format("15:04:05"), e.Step)
			}
		default:
			fmt.Fprintln(cmd.ErrOrStderr(), e.String())
		}
	}
}
