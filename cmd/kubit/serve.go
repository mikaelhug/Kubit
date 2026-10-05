package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/mikael/kubit/internal/api"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/watch"
	"github.com/spf13/cobra"
)

func serveCmd(openConsole bool) *cobra.Command {
	var addr, token, configPath string
	var interval, serviceInterval time.Duration
	var open bool
	cmd := &cobra.Command{
		Use:   "serve [dirs...]",
		Short: "Run the Kubit daemon and web console, observing the cluster repos in dirs",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			settings, err := loadServeConfig(configPath)
			if err != nil {
				return err
			}
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
			if err := m.Store.PutSettings(cmd.Context(), settings); err != nil {
				return err
			}
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
			srv := api.New(version, m, token, crypto)
			srv.Start()
			ctx, stop := context.WithCancel(cmd.Context())
			defer stop()
			w := watch.New(m, interval)
			if serviceInterval > 0 {
				w.ServiceInterval = serviceInterval
			}
			srv.AttachWatcher(ctx, w)
			srv.AttachStop(stop)
			if err := srv.ServeRepos(ctx, args); err != nil {
				return err
			}
			hs := &http.Server{Handler: srv}
			errc := make(chan error, 1)
			go func() { errc <- hs.Serve(ln) }()
			fmt.Fprintf(cmd.OutOrStdout(), "kubit %s listening on http://%s (master key from %s); Ctrl-C stops it\n", version, ln.Addr(), store.MasterKeySource())
			if open {
				if u := consoleURL(ln.Addr()); openBrowser(u) != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "open %s in a browser\n", u)
				}
			}
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
			if err := m.Store.Checkpoint(context.Background()); err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "kubit:", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8090", "listen address")
	cmd.Flags().StringVar(&configPath, "config", "", "YAML file with alert, PXE and factory settings; "+envSMTPPassword+" holds the SMTP password")
	cmd.Flags().StringVar(&token, "token", "", "API bearer token (generated when binding beyond loopback)")
	cmd.Flags().DurationVar(&interval, "watch-interval", 15*time.Second, "how often every cluster is polled for health samples and events")
	cmd.Flags().DurationVar(&serviceInterval, "service-interval", 0, "how often workloads, pods, claims and services are inspected for alerts (default 4× watch-interval)")
	cmd.Flags().BoolVar(&open, "open", openConsole, "open the console in the default browser once listening")
	return cmd
}

func consoleURL(addr net.Addr) string {
	host, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		return "http://" + addr.String()
	}
	if ip := net.ParseIP(host); ip == nil || ip.IsUnspecified() {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Run()
	case "linux":
		return exec.Command("xdg-open", url).Start()
	}
	return fmt.Errorf("no browser opener on %s", runtime.GOOS)
}
