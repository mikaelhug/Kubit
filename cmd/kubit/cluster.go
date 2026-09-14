package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/config"
	"github.com/spf13/cobra"
)

func clusterCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "cluster", Short: "Create and inspect clusters"}
	cmd.AddCommand(clusterCreateCmd(), clusterListCmd(), clusterGetCmd(), clusterCredsCmd("kubeconfig"), clusterCredsCmd("talosconfig"), clusterForgetCmd(), clusterApplyCmd(), clusterExportCmd())
	return cmd
}

func printEvents(cmd *cobra.Command) cluster.Sink {
	return func(e cluster.Event) {
		switch e.Kind {
		case cluster.KindSteps:
			// The CLI shows steps as they run rather than up front.
		case cluster.KindStep:
			if e.Status == cluster.StepRunning {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s ▶ %s\n", e.Time.Format("15:04:05"), e.Step)
			} else if e.Status == cluster.StepFailed {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s ✗ %s\n", e.Time.Format("15:04:05"), e.Step)
			}
		default:
			fmt.Fprintln(cmd.ErrOrStderr(), e.String())
		}
	}
}

func clusterCreateCmd() *cobra.Command {
	var file string
	var skipPlatform bool
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Provision a cluster from cluster.yaml on nodes in maintenance mode",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := config.Load(file)
			if err != nil {
				return err
			}
			m, err := openManager()
			if err != nil {
				return err
			}
			defer m.Store.Close()
			if err := m.Create(cmd.Context(), c, printEvents(cmd)); err != nil {
				return err
			}
			if skipPlatform {
				return nil
			}
			return m.ApplyPlatform(cmd.Context(), c.Metadata.Name, printEvents(cmd))
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "cluster.yaml", "cluster declaration")
	cmd.Flags().BoolVar(&skipPlatform, "skip-platform", false, "stop after the Kubernetes API is up; do not apply platform add-ons")
	return cmd
}

func clusterListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List clusters",
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()
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
		},
	}
}

func clusterGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <name>",
		Short: "Print the stored cluster.yaml",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()
			r, err := s.GetCluster(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(r.Spec)
			return err
		},
	}
}

func clusterCredsCmd(kind string) *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   kind + " <name>",
		Short: "Write the cluster's " + kind + " to a file (or stdout with -o -)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()
			sec, err := s.GetClusterSecrets(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			data := sec.Talosconfig
			if kind == "kubeconfig" {
				data = sec.Kubeconfig
			}
			if data == nil {
				return fmt.Errorf("cluster %s has no %s yet", args[0], kind)
			}
			if out == "-" {
				_, err = cmd.OutOrStdout().Write(data)
				return err
			}
			if out == "" {
				out = kind
			}
			return os.WriteFile(out, data, 0o600)
		},
	}
	cmd.Flags().StringVarP(&out, "out", "o", "", "output file (default ./"+kind+"; - for stdout)")
	return cmd
}

func clusterForgetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "forget <name>",
		Short: "Drop a cluster and its secrets from the store without touching the nodes",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()
			if _, err := s.GetCluster(cmd.Context(), args[0]); err != nil {
				return err
			}
			_ = s.Audit(cmd.Context(), args[0], "cluster.forget", "")
			return s.DeleteCluster(cmd.Context(), args[0])
		},
	}
}

func clusterApplyCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "apply <name>",
		Short: "Re-apply machine configs regenerated from the stored cluster.yaml (or -f to update it first)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := openManager()
			if err != nil {
				return err
			}
			defer m.Store.Close()
			c, row, err := m.LoadCluster(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if file != "" {
				updated, err := config.Load(file)
				if err != nil {
					return err
				}
				if updated.Metadata.Name != c.Metadata.Name {
					return fmt.Errorf("%s declares cluster %q, not %q", file, updated.Metadata.Name, c.Metadata.Name)
				}
				updated.Spec.SchematicID = c.Spec.SchematicID
				if err := m.SaveCluster(cmd.Context(), updated, row.State); err != nil {
					return err
				}
				c = updated
			}
			if err := m.ApplyConfigs(cmd.Context(), c, "", printEvents(cmd)); err != nil {
				return err
			}
			return m.ApplyPlatform(cmd.Context(), c.Metadata.Name, printEvents(cmd))
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "updated cluster.yaml to store before applying")
	return cmd
}

func clusterExportCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "export <name>",
		Short: "Write native Talos artefacts and an OpenTofu (siderolabs/talos) root for the cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := openManager()
			if err != nil {
				return err
			}
			defer m.Store.Close()
			if out == "" {
				out = "export-" + args[0]
			}
			if err := m.Export(cmd.Context(), args[0], out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "exported cluster %s to %s\n", args[0], out)
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "out", "o", "", "output directory (default ./export-<name>)")
	return cmd
}
