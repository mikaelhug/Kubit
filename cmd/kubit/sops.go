package main

import (
	"fmt"

	"github.com/mikael/kubit/internal/repo"
	"github.com/spf13/cobra"
)

func recipientCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "recipient [dir]",
		Short: "Print the age recipient Flux decrypts app secrets with, for the apps repo's .sops.yaml",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := repo.Load(dirArg(args))
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), r.Secrets.FluxRecipient())
			return err
		},
	}
}
