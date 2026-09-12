package main

import "github.com/spf13/cobra"

func upgradeCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "upgrade", Short: "Rolling Talos and Kubernetes upgrades"}
	var clusterName, to string
	talosCmd := &cobra.Command{
		Use:   "talos",
		Short: "Upgrade Talos node by node (control planes first)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := openManager()
			if err != nil {
				return err
			}
			defer m.Store.Close()
			return m.UpgradeTalos(cmd.Context(), clusterName, to, printEvents(cmd))
		},
	}
	k8sCmd := &cobra.Command{
		Use:   "kubernetes",
		Short: "Upgrade Kubernetes components node by node (control planes first)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := openManager()
			if err != nil {
				return err
			}
			defer m.Store.Close()
			return m.UpgradeKubernetes(cmd.Context(), clusterName, to, printEvents(cmd))
		},
	}
	for _, c := range []*cobra.Command{talosCmd, k8sCmd} {
		c.Flags().StringVar(&clusterName, "cluster", "", "cluster to upgrade")
		c.Flags().StringVar(&to, "to", "", "target version, e.g. v1.14.1")
		_ = c.MarkFlagRequired("cluster")
		_ = c.MarkFlagRequired("to")
	}
	cmd.AddCommand(talosCmd, k8sCmd)
	return cmd
}
