package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func sopsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "sops", Short: "The age key Flux uses to decrypt SOPS files in a cluster"}
	recipient := &cobra.Command{
		Use:   "recipient <cluster>",
		Short: "Print the cluster's age recipient for .sops.yaml",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := openManager()
			if err != nil {
				return err
			}
			defer m.Store.Close()
			k, err := m.SOPSKey(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), k.Recipient)
			return nil
		},
	}
	var out string
	var force bool
	export := &cobra.Command{
		Use:   "export <cluster>",
		Short: "Write the cluster's private age key to a file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := openManager()
			if err != nil {
				return err
			}
			defer m.Store.Close()
			k, err := m.SOPSKey(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
			if force {
				flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
			}
			f, err := os.OpenFile(out, flags, 0o600)
			if err != nil {
				return err
			}
			if err := f.Chmod(0o600); err != nil {
				f.Close()
				return err
			}
			if _, err := f.Write(k.Identity); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
			_ = m.Store.Audit(cmd.Context(), args[0], "sops.export", k.Recipient)
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s (%s)\n", out, k.Recipient)
			return nil
		},
	}
	export.Flags().StringVarP(&out, "out", "o", "keys.txt", "output file")
	export.Flags().BoolVar(&force, "force", false, "overwrite an existing file")
	imp := &cobra.Command{
		Use:   "import <cluster> <file>",
		Short: "Replace the cluster's age key; it reaches the cluster on the next platform apply",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := os.ReadFile(args[1])
			if err != nil {
				return err
			}
			m, err := openManager()
			if err != nil {
				return err
			}
			defer m.Store.Close()
			k, err := m.ImportSOPSKey(cmd.Context(), args[0], b)
			if err != nil {
				return err
			}
			_ = m.Store.Audit(cmd.Context(), args[0], "sops.import", k.Recipient)
			fmt.Fprintln(cmd.OutOrStdout(), k.Recipient)
			return nil
		},
	}
	cmd.AddCommand(recipient, export, imp)
	return cmd
}
