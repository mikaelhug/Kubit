package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/mikael/kubit/internal/api"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/watch"
	"github.com/spf13/cobra"
)

func serveCmd() *cobra.Command {
	var addr, token string
	var interval, serviceInterval time.Duration
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the Kubit daemon and web UI",
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := homeDir()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(home, 0o700); err != nil {
				return err
			}
			lock, err := store.LockHome(home)
			if err != nil {
				return err
			}
			defer lock.Release()
			m, crypto, err := openManagerCrypto()
			if err != nil {
				return err
			}
			defer m.Store.Close()
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return err
			}
			defer ln.Close()
			if !api.Loopback(addr) && token == "" {
				token = os.Getenv("KUBIT_TOKEN")
				if token == "" {
					b := make([]byte, 16)
					_, _ = rand.Read(b)
					token = hex.EncodeToString(b)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "non-loopback bind: API requires Authorization: Bearer %s\n", token)
			}
			mode := "foreground"
			if os.Getenv("KUBIT_SERVICE") != "" {
				mode = "service"
			}
			srv := api.New(version, m, token, crypto)
			srv.Start()
			ctx := cmd.Context()
			w := watch.New(m, interval)
			if serviceInterval > 0 {
				w.ServiceInterval = serviceInterval
			}
			srv.AttachWatcher(ctx, w)
			hs := &http.Server{Handler: srv}
			errc := make(chan error, 1)
			go func() { errc <- hs.Serve(ln) }()
			fmt.Fprintf(cmd.OutOrStdout(), "kubit %s listening on http://%s (%s, master key from %s)\n", version, ln.Addr(), mode, store.MasterKeySource())
			select {
			case err := <-errc:
				return err
			case <-ctx.Done():
			}
			fmt.Fprintln(cmd.OutOrStdout(), "kubit: shutting down")
			srv.Drain(10 * time.Second)
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = hs.Shutdown(shutdown)
			return m.Store.Checkpoint(context.Background())
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8090", "listen address")
	cmd.Flags().StringVar(&token, "token", "", "API bearer token (generated when binding beyond loopback)")
	cmd.Flags().DurationVar(&interval, "watch-interval", 15*time.Second, "how often every cluster is polled for health samples and events")
	cmd.Flags().DurationVar(&serviceInterval, "service-interval", 0, "how often workloads, pods, claims and services are inspected for alerts (default 4× watch-interval)")
	return cmd
}
