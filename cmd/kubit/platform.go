package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func platformCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "platform", Short: "Converge in-cluster add-ons via OpenTofu"}
	plan := &cobra.Command{
		Use:   "plan <cluster>",
		Short: "Render infra/platform and show what would change (drift check)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := openManager()
			if err != nil {
				return err
			}
			defer m.Store.Close()
			sum, err := m.PlanPlatform(cmd.Context(), args[0], printEvents(cmd))
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), sum)
			return nil
		},
	}
	apply := &cobra.Command{
		Use:   "apply <cluster>",
		Short: "Apply the platform layer declared in cluster.yaml",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := openManager()
			if err != nil {
				return err
			}
			defer m.Store.Close()
			return m.ApplyPlatform(cmd.Context(), args[0], printEvents(cmd))
		},
	}
	cmd.AddCommand(plan, apply)
	return cmd
}
