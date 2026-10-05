package main

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
	"github.com/spf13/cobra"
)

func nodeCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "node", Short: "Inspect nodes"}
	cmd.AddCommand(nodeListCmd(), nodeServicesCmd(), nodeLogsCmd())
	return cmd
}

func nodeListCmd() *cobra.Command {
	var clusterName string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List known nodes (discovered and cluster members)",
		RunE: withStore(func(cmd *cobra.Command, _ []string, s *store.Store) error {
			rows, err := s.ListNodes(cmd.Context(), clusterName)
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "IP\tCLUSTER\tHOSTNAME\tROLE\tSTATE\tARCH\tMAC\tCPU\tRAM\tKVM\tLAST SEEN")
			for _, r := range rows {
				var inv talos.Inventory
				_ = json.Unmarshal(r.Hardware, &inv)
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\t%s\t%v\t%s\n",
					r.IP, r.Cluster, r.Hostname, r.Role, r.State, r.Arch, r.MAC, inv.CPUs, cluster.HumanBytes(inv.MemoryBytes), inv.KVM, r.LastSeen)
			}
			return tw.Flush()
		}),
	}
	cmd.Flags().StringVar(&clusterName, "cluster", "", "only nodes of this cluster")
	return cmd
}
