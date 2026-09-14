package main

import (
	"encoding/base64"
	"fmt"
	"os"

	"github.com/mikael/kubit/internal/store"
	"github.com/spf13/cobra"
)

func backupCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Write a sealed backup of ~/.kubit (database, cluster secrets and infra roots)",
		Long: `The archive is encrypted with the master key from the Keychain. Restoring on another
machine needs that key: run 'kubit key export' here and set KUBIT_MASTER_KEY there.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, crypto, err := openManagerCrypto()
			if err != nil {
				return err
			}
			defer m.Store.Close()
			if err := m.Store.Checkpoint(cmd.Context()); err != nil {
				return err
			}
			f, err := os.OpenFile(out, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
			if err != nil {
				return err
			}
			defer f.Close()
			if err := store.Backup(m.Home, crypto, f); err != nil {
				return err
			}
			_ = m.Store.Audit(cmd.Context(), "", "backup", out)
			fmt.Fprintf(cmd.OutOrStdout(), "backup written to %s\n", out)
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "out", "o", "kubit-backup.kubitbak", "output file")
	return cmd
}

func restoreCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "restore <file>",
		Short: "Restore ~/.kubit from a backup (the daemon must not be running)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := homeDir()
			if err != nil {
				return err
			}
			crypto, err := store.LoadCrypto()
			if err != nil {
				return err
			}
			f, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer f.Close()
			if err := os.MkdirAll(home, 0o700); err != nil {
				return err
			}
			if err := store.Restore(home, crypto, f, force); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "restored into %s\n", home)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing Kubit home")
	return cmd
}

func keyCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "key", Short: "Master key operations"}
	cmd.AddCommand(&cobra.Command{
		Use:   "export",
		Short: "Print the master key (base64) for KUBIT_MASTER_KEY on another machine — treat it like the cluster secrets",
		RunE: func(cmd *cobra.Command, _ []string) error {
			key, err := store.LoadMasterKey()
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), base64.StdEncoding.EncodeToString(key))
			return nil
		},
	})
	return cmd
}
