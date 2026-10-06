package main

import (
	"fmt"
	"text/tabwriter"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/spf13/cobra"
)

func etcdCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "etcd", Short: "etcd snapshots in the repo's snapshots/ directory: take, list, restore"}
	snapshot := &cobra.Command{
		Use:   "snapshot [dir]",
		Short: "Take a verified etcd snapshot, encrypted to the repo's age recipients",
		Args:  cobra.MaximumNArgs(1),
		RunE: withManager(func(cmd *cobra.Command, args []string, m *cluster.Manager) error {
			name, err := useCluster(cmd, m, dirArg(args))
			if err != nil {
				return err
			}
			sn, err := m.SnapshotEtcd(cmd.Context(), name, "manual", printEvents(cmd))
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "snapshot %s: %d bytes\n", sn.ID, sn.SizeBytes)
			return nil
		}),
	}
	list := &cobra.Command{
		Use:   "list [dir]",
		Short: "List the repo's snapshots",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			list, err := cluster.SnapshotsIn(dirArg(args))
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tTIME\tSIZE")
			for _, s := range list {
				fmt.Fprintf(tw, "%s\t%s\t%d\n", s.ID, s.TS, s.SizeBytes)
			}
			return tw.Flush()
		},
	}
	var yes bool
	restore := &cobra.Command{
		Use:   "restore <dir> <id>",
		Short: "DESTRUCTIVE: wipe etcd on every control plane and rebuild it from the snapshot",
		Args:  cobra.ExactArgs(2),
		RunE: withManager(func(cmd *cobra.Command, args []string, m *cluster.Manager) error {
			if !yes {
				return fmt.Errorf("restore wipes etcd state on every control plane of %s; re-run with --yes", args[0])
			}
			name, err := useCluster(cmd, m, args[0])
			if err != nil {
				return err
			}
			return m.RestoreEtcd(cmd.Context(), name, args[1], printEvents(cmd))
		}),
	}
	restore.Flags().BoolVar(&yes, "yes", false, "confirm the destructive restore")
	cmd.AddCommand(snapshot, list, restore)
	return cmd
}
