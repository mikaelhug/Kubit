package main

import (
	"fmt"
	"strconv"
	"text/tabwriter"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/spf13/cobra"
)

func etcdCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "etcd", Short: "Local etcd snapshots for disaster recovery: take, list, restore"}
	snapshot := &cobra.Command{
		Use:   "snapshot <dir|cluster>",
		Short: "Take and store a verified, sealed etcd snapshot now",
		Args:  cobra.ExactArgs(1),
		RunE: withManager(func(cmd *cobra.Command, args []string, m *cluster.Manager) error {
			name, err := useCluster(cmd, m, args[0])
			if err != nil {
				return err
			}
			sn, err := m.SnapshotEtcd(cmd.Context(), name, "manual", printEvents(cmd))
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "snapshot #%d: %d bytes, %d keys, from %s\n", sn.ID, sn.SizeBytes, sn.Keys, sn.Node)
			return nil
		}),
	}
	list := &cobra.Command{
		Use:   "list <dir|cluster>",
		Short: "List stored snapshots",
		Args:  cobra.ExactArgs(1),
		RunE: withManager(func(cmd *cobra.Command, args []string, m *cluster.Manager) error {
			name, err := nameOf(args[0])
			if err != nil {
				return err
			}
			list, err := m.Store.ListSnapshots(cmd.Context(), name)
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tTIME\tNODE\tSIZE\tKEYS\tSOURCE\tSTATUS")
			for _, s := range list {
				fmt.Fprintf(tw, "%d\t%s\t%s\t%d\t%d\t%s\t%s\n", s.ID, s.TS, s.Node, s.SizeBytes, s.Keys, s.Source, s.Status)
			}
			return tw.Flush()
		}),
	}
	var yes bool
	restore := &cobra.Command{
		Use:   "restore <dir|cluster> <id>",
		Short: "DESTRUCTIVE: wipe etcd on every control plane and rebuild it from the snapshot",
		Args:  cobra.ExactArgs(2),
		RunE: withManager(func(cmd *cobra.Command, args []string, m *cluster.Manager) error {
			if !yes {
				return fmt.Errorf("restore wipes etcd state on every control plane of %s; re-run with --yes", args[0])
			}
			id, err := strconv.ParseInt(args[1], 10, 64)
			if err != nil {
				return err
			}
			name, err := useCluster(cmd, m, args[0])
			if err != nil {
				return err
			}
			return m.RestoreEtcd(cmd.Context(), name, id, printEvents(cmd))
		}),
	}
	restore.Flags().BoolVar(&yes, "yes", false, "confirm the destructive restore")
	cmd.AddCommand(snapshot, list, restore)
	return cmd
}
