package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/mikael/kubit/internal/api"
	"github.com/mikael/kubit/internal/watch"
	"github.com/spf13/cobra"
)

func serveCmd() *cobra.Command {
	var addr, token string
	var interval time.Duration
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the Kubit daemon and web UI",
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, crypto, err := openManagerCrypto()
			if err != nil {
				return err
			}
			defer m.Store.Close()
			if !api.Loopback(addr) && token == "" {
				token = os.Getenv("KUBIT_TOKEN")
				if token == "" {
					b := make([]byte, 16)
					_, _ = rand.Read(b)
					token = hex.EncodeToString(b)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "non-loopback bind: API requires Authorization: Bearer %s\n", token)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "kubit %s listening on http://%s\n", version, addr)
			srv := api.New(version, m, token, crypto)
			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()
			srv.AttachWatcher(ctx, watch.New(m, interval))
			return http.ListenAndServe(addr, srv)
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8080", "listen address")
	cmd.Flags().StringVar(&token, "token", "", "API bearer token (generated when binding beyond loopback)")
	cmd.Flags().DurationVar(&interval, "watch-interval", 15*time.Second, "how often every cluster is polled for health samples and events")
	return cmd
}
