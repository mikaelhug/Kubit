package main

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
	"github.com/spf13/cobra"
)

func nodeCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "node", Short: "Manage nodes"}
	cmd.AddCommand(nodeListCmd(), nodeAddCmd(), nodeRemoveCmd(), nodeServicesCmd(), nodeLogsCmd())
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

func nodeAddCmd() *cobra.Command {
	var (
		n           config.Node
		clusterName string
		disk        string
		role        string
		arch        string
	)
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Join a maintenance-mode machine to an existing cluster",
		RunE: withManager(func(cmd *cobra.Command, _ []string, m *cluster.Manager) error {
			n.Role = config.Role(role)
			n.Arch = config.Arch(arch)
			n.InstallDisk = config.InstallDisk{Path: disk}
			if row, err := m.Store.GetNode(cmd.Context(), n.IP); err == nil {
				if n.MAC == "" {
					n.MAC = row.MAC
				}
				var inv talos.Inventory
				if json.Unmarshal(row.Hardware, &inv) == nil {
					if !cmd.Flags().Changed("tpm") {
						n.TPM = inv.TPM
					}
					if !cmd.Flags().Changed("watchdog") {
						n.Watchdog = inv.Watchdog
					}
				}
			}
			return m.AddNode(cmd.Context(), clusterName, n, printEvents(cmd))
		}),
	}
	cmd.Flags().StringVar(&clusterName, "cluster", "", "target cluster")
	cmd.Flags().StringVar(&n.IP, "ip", "", "node IP (in maintenance mode)")
	cmd.Flags().StringVar(&n.Hostname, "hostname", "", "hostname to assign")
	cmd.Flags().StringVar(&n.MAC, "mac", "", "uplink MAC (default: from discovery)")
	cmd.Flags().StringVar(&role, "role", "worker", "controlplane | worker")
	cmd.Flags().StringVar(&arch, "arch", "amd64", "amd64 | arm64")
	cmd.Flags().StringVar(&disk, "disk", "", "install disk device path")
	cmd.Flags().BoolVar(&n.KVM, "kvm", false, "node has /dev/kvm (enables runsc-kvm)")
	cmd.Flags().BoolVar(&n.TPM, "tpm", false, "node has a TPM 2.0 on UEFI (default: from discovery)")
	cmd.Flags().BoolVar(&n.Watchdog, "watchdog", false, "node has /dev/watchdog0 (default: from discovery)")
	for _, f := range []string{"cluster", "ip", "hostname", "disk"} {
		_ = cmd.MarkFlagRequired(f)
	}
	return cmd
}

func nodeRemoveCmd() *cobra.Command {
	var clusterName string
	var force bool
	cmd := &cobra.Command{
		Use:   "remove <hostname>",
		Short: "Drain a node, delete it from Kubernetes and reset Talos back to maintenance mode",
		Args:  cobra.ExactArgs(1),
		RunE: withManager(func(cmd *cobra.Command, args []string, m *cluster.Manager) error {
			return m.RemoveNode(cmd.Context(), clusterName, args[0], cluster.RemoveOptions{Force: force}, printEvents(cmd))
		}),
	}
	cmd.Flags().StringVar(&clusterName, "cluster", "", "cluster the node belongs to")
	cmd.Flags().BoolVar(&force, "force", false, "skip the quorum guard and tolerate an unreachable node")
	_ = cmd.MarkFlagRequired("cluster")
	return cmd
}
