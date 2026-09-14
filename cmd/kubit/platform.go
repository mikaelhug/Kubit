package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 80 {
		return s[:77] + "..."
	}
	if s == "" {
		return "(none)"
	}
	return s
}

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
			diff, err := m.PlanPlatform(cmd.Context(), args[0], printEvents(cmd))
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			for _, g := range diff.Groups {
				fmt.Fprintf(w, "%s\n", g.Addon)
				for _, c := range g.Changes {
					fmt.Fprintf(w, "  %-8s %s\n", c.Action, c.Address)
					for _, a := range c.Attrs {
						switch {
						case a.Sensitive:
							fmt.Fprintf(w, "           %s: (sensitive)\n", a.Key)
						case a.Unknown:
							fmt.Fprintf(w, "           %s: (known after apply)\n", a.Key)
						default:
							fmt.Fprintf(w, "           %s: %s → %s\n", a.Key, oneLine(a.Before), oneLine(a.After))
						}
					}
				}
			}
			for _, wn := range diff.Warnings {
				fmt.Fprintf(w, "warning: %s\n", wn)
			}
			fmt.Fprintln(w, diff.Summary)
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
