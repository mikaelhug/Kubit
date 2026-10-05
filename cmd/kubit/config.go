package main

import (
	"github.com/mikael/kubit/internal/config"
	"github.com/spf13/cobra"
)

func configCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Work with cluster.yaml"}
	cmd.AddCommand(configValidateCmd())
	return cmd
}

func configValidateCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Parse and validate a cluster.yaml, printing the defaulted result",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := config.Load(file)
			if err != nil {
				return err
			}
			b, err := c.Marshal()
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(b)
			return err
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "cluster.yaml", "cluster declaration")
	return cmd
}
