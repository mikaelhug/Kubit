package main

import (
	"fmt"
	"net/http"

	"github.com/mikael/kubit/internal/api"
	"github.com/spf13/cobra"
)

func serveCmd() *cobra.Command {
	var addr string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the Kubit daemon and web UI",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "kubit %s listening on http://%s\n", version, addr)
			return http.ListenAndServe(addr, api.New(version))
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8080", "listen address")
	return cmd
}
