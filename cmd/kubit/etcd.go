package main

import (
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func etcdCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "etcd", Short: "etcd snapshots: take, list, verify, download, restore"}
	snapshot := &cobra.Command{
		Use:   "snapshot <cluster>",
		Short: "Take and store a verified, sealed etcd snapshot now",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := openManager()
			if err != nil {
				return err
			}
			defer m.Store.Close()
			sn, err := m.SnapshotEtcd(cmd.Context(), args[0], "manual", printEvents(cmd))
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "snapshot #%d: %d bytes, %d keys, from %s\n", sn.ID, sn.SizeBytes, sn.Keys, sn.Node)
			return nil
		},
	}
	list := &cobra.Command{
		Use:   "list <cluster>",
		Short: "List stored snapshots",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := openManager()
			if err != nil {
				return err
			}
			defer m.Store.Close()
			list, err := m.Store.ListSnapshots(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tTIME\tNODE\tSIZE\tKEYS\tSOURCE\tSTATUS")
			for _, s := range list {
				fmt.Fprintf(tw, "%d\t%s\t%s\t%d\t%d\t%s\t%s\n", s.ID, s.TS, s.Node, s.SizeBytes, s.Keys, s.Source, s.Status)
			}
			return tw.Flush()
		},
	}
	var out string
	download := &cobra.Command{
		Use:   "download <cluster> <id>",
		Short: "Write the plain snapshot to a file (for talosctl bootstrap --recover-from or etcdutl)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[1], 10, 64)
			if err != nil {
				return err
			}
			m, err := openManager()
			if err != nil {
				return err
			}
			defer m.Store.Close()
			sn, plain, err := m.OpenSnapshot(cmd.Context(), id)
			if err != nil {
				return err
			}
			if sn.Cluster != args[0] {
				return fmt.Errorf("snapshot %d belongs to %s", id, sn.Cluster)
			}
			if out == "" {
				out = fmt.Sprintf("%s-etcd-%d.db", sn.Cluster, sn.ID)
			}
			return os.WriteFile(out, plain, 0o600)
		},
	}
	download.Flags().StringVarP(&out, "out", "o", "", "output file")
	var yes bool
	restore := &cobra.Command{
		Use:   "restore <cluster> <id>",
		Short: "DESTRUCTIVE: wipe etcd on every control plane and rebuild it from the snapshot",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				return fmt.Errorf("restore wipes etcd state on every control plane of %s; re-run with --yes", args[0])
			}
			id, err := strconv.ParseInt(args[1], 10, 64)
			if err != nil {
				return err
			}
			m, err := openManager()
			if err != nil {
				return err
			}
			defer m.Store.Close()
			return m.RestoreEtcd(cmd.Context(), args[0], id, printEvents(cmd))
		},
	}
	restore.Flags().BoolVar(&yes, "yes", false, "confirm the destructive restore")
	cmd.AddCommand(snapshot, list, download, restore)
	return cmd
}
