package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
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
			m, crypto, err := openManagerCrypto()
			if err != nil {
				return err
			}
			defer m.Store.Close()
			lock, err := store.LockHome(m.Home)
			if err != nil {
				return err
			}
			defer lock.Release()
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
			fmt.Fprintf(cmd.OutOrStdout(), "kubit %s listening on http://%s (%s, master key from %s)\n", version, addr, mode, store.MasterKeySource())
			srv := api.New(version, m, token, crypto)
			// SIGTERM/SIGINT: stop the watcher, cancel running operations so they are
			// recorded as cancelled, close the listener and fold the WAL.
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			w := watch.New(m, interval)
			if serviceInterval > 0 {
				w.ServiceInterval = serviceInterval
			}
			srv.AttachWatcher(ctx, w)
			hs := &http.Server{Addr: addr, Handler: srv}
			errc := make(chan error, 1)
			go func() { errc <- hs.ListenAndServe() }()
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
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8080", "listen address")
	cmd.Flags().StringVar(&token, "token", "", "API bearer token (generated when binding beyond loopback)")
	cmd.Flags().DurationVar(&interval, "watch-interval", 15*time.Second, "how often every cluster is polled for health samples and events")
	cmd.Flags().DurationVar(&serviceInterval, "service-interval", 0, "how often workloads, pods, claims and services are inspected for alerts (default 4× watch-interval)")
	return cmd
}
