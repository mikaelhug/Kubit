package main

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/repo"
	"github.com/mikael/kubit/internal/sops"
	"github.com/spf13/cobra"
)

func exportCmd() *cobra.Command {
	var dir string
	var recipients []string
	cmd := &cobra.Command{
		Use:   "export <cluster> --repo <dir>",
		Short: "Move a cluster kept in ~/.kubit into a repo: cluster.yaml, encrypted secrets, platform state",
		Args:  cobra.ExactArgs(1),
		RunE: withManager(func(cmd *cobra.Command, args []string, m *cluster.Manager) error {
			if dir == "" {
				return errors.New("--repo: the directory to write the cluster repo to")
			}
			var err error
			if len(recipients) == 0 {
				if recipients, err = ownRecipients(cmd.ErrOrStderr()); err != nil {
					return err
				}
			}
			r, moved, err := m.ExportRepo(cmd.Context(), args[0], dir, recipients)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "wrote %s, %s, %s\n", filepath.Join(dir, repo.ClusterFile), repo.SecretsFile, sops.ConfigFile)
			if moved {
				fmt.Fprintf(out, "moved the platform state to %s, encrypted\n", r.StatePath())
			}
			fmt.Fprintf(out, "Run kubit plan %s; it should find nothing to do. Then commit the repo and run kubit %s.\n", dir, dir)
			return nil
		}),
	}
	cmd.Flags().StringVar(&dir, "repo", "", "directory to write the cluster repo to")
	cmd.Flags().StringSliceVar(&recipients, "age", nil, "age recipients for the secrets (default: .sops.yaml there, else your own key)")
	return cmd
}
