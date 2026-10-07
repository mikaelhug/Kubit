package main

import (
	"fmt"

	"github.com/mikaelhug/kubit/internal/repo"
	"github.com/mikaelhug/kubit/internal/sops"
	"github.com/spf13/cobra"
)

func deployKeyCmd() *cobra.Command {
	var hostsOnly bool
	cmd := &cobra.Command{
		Use:   "deploy-key [dir]",
		Short: "Write a new SSH deploy key for platform.flux.repository and its host keys into secrets.sops.yaml",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := dirArg(args)
			ids, err := sops.Identities()
			if err != nil {
				return err
			}
			k, err := repo.DeployKeyOf(dir, ids)
			if err != nil {
				return err
			}
			if err := repo.NewDeployKey(cmd.Context(), dir, k.Hash, hostsOnly, ids); err != nil {
				return err
			}
			if k, err = repo.DeployKeyOf(dir, ids); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if k.Key != nil {
				fmt.Fprintf(out, "%s\n%s\n", k.Public, k.Fingerprint)
			}
			for _, h := range k.Hosts {
				fmt.Fprintf(out, "%s %s %s\n", h.Host, h.Type, h.Fingerprint)
			}
			if !hostsOnly {
				fmt.Fprintln(out, "Add the key to the repository's deploy keys (read-only), commit, then run kubit apply.")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&hostsOnly, "hosts", false, "rescan the host keys only")
	return cmd
}
