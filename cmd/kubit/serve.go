package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/mikael/kubit/internal/api"
	"github.com/mikael/kubit/internal/repo"
	"github.com/mikael/kubit/internal/watch"
	"github.com/spf13/cobra"
)

func serveCmd(openConsole bool) *cobra.Command {
	var addr, token, configPath string
	var interval time.Duration
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
			m, err := openManager()
			if err != nil {
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
			settings.PXERunDir = filepath.Join(home, "run")
			ctx, stop := context.WithCancel(cmd.Context())
			defer stop()
			srv := api.New(version, m, token, settings, watch.New(m, interval), stop)
			srv.Start(ctx)
			if err := srv.ServeRepos(ctx, args); err != nil {
				return err
			}
			hs := &http.Server{Handler: srv}
			errc := make(chan error, 1)
			go func() { errc <- hs.Serve(ln) }()
			fmt.Fprintf(cmd.OutOrStdout(), "kubit %s listening on http://%s; Ctrl-C stops it\n", version, ln.Addr())
			if open {
				if u := consoleURL(ln.Addr()) + consolePath(args); openBrowser(u) != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "open %s in a browser\n", u)
				}
			}
			select {
			case err := <-errc:
				return err
			case <-ctx.Done():
			}
			fmt.Fprintln(cmd.OutOrStdout(), "kubit: shutting down")
			srv.Close()
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = hs.Shutdown(shutdown)
			return nil
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8090", "listen address")
	cmd.Flags().StringVar(&configPath, "config", "", "YAML file with alert, PXE and factory settings")
	cmd.Flags().StringVar(&token, "token", "", "API bearer token (generated when binding beyond loopback)")
	cmd.Flags().DurationVar(&interval, "watch-interval", 15*time.Second, "how often every cluster is observed")
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

func consolePath(dirs []string) string {
	var names []string
	for _, d := range dirs {
		if c, _, err := repo.LoadSpec(d); err == nil {
			names = append(names, c.Metadata.Name)
		}
	}
	if len(names) != 1 {
		return "/"
	}
	return "/clusters/" + url.PathEscape(names[0]) + "/overview"
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
