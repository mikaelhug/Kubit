package main

import (
	"fmt"
	"text/tabwriter"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/store"
	"github.com/spf13/cobra"
)

func clusterCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "cluster", Short: "Inspect clusters cached in ~/.kubit"}
	cmd.AddCommand(clusterListCmd(), clusterGetCmd(), clusterForgetCmd(), clusterExportCmd())
	return cmd
}

func clusterListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List clusters",
		RunE: withStore(func(cmd *cobra.Command, _ []string, s *store.Store) error {
			rows, err := s.ListClusters(cmd.Context())
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tSTATE\tTALOS\tKUBERNETES\tNODES\tUPDATED")
			for _, r := range rows {
				c, err := config.Parse(r.Spec)
				if err != nil {
					fmt.Fprintf(tw, "%s\t%s\t(invalid spec: %v)\n", r.Name, r.State, err)
					continue
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\n", r.Name, r.State, c.Spec.TalosVersion, c.Spec.KubernetesVersion, len(c.Spec.Nodes), r.UpdatedAt)
			}
			return tw.Flush()
		}),
	}
}

func clusterGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <name>",
		Short: "Print the stored cluster.yaml",
		Args:  cobra.ExactArgs(1),
		RunE: withStore(func(cmd *cobra.Command, args []string, s *store.Store) error {
			r, err := s.GetCluster(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(r.Spec)
			return err
		}),
	}
}

func clusterForgetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "forget <name>",
		Short: "Drop a cluster and its secrets from the store without touching the nodes",
		Args:  cobra.ExactArgs(1),
		RunE: withStore(func(cmd *cobra.Command, args []string, s *store.Store) error {
			if _, err := s.GetCluster(cmd.Context(), args[0]); err != nil {
				return err
			}
			_ = s.Audit(cmd.Context(), args[0], "cluster.forget", "")
			return s.DeleteCluster(cmd.Context(), args[0])
		}),
	}
}

func clusterExportCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "export <dir|cluster>",
		Short: "Write native Talos artefacts and an OpenTofu (siderolabs/talos) root for the cluster",
		Args:  cobra.ExactArgs(1),
		RunE: withManager(func(cmd *cobra.Command, args []string, m *cluster.Manager) error {
			name, err := useCluster(cmd, m, args[0])
			if err != nil {
				return err
			}
			if out == "" {
				out = "export-" + name
			}
			if err := m.Export(cmd.Context(), name, out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "exported cluster %s to %s\n", name, out)
			return nil
		}),
	}
	cmd.Flags().StringVarP(&out, "out", "o", "", "output directory (default ./export-<name>)")
	return cmd
}
