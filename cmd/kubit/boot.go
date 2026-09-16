package main

import (
	"fmt"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mikael/kubit/internal/boot"
	"github.com/mikael/kubit/internal/oob"
	"github.com/mikael/kubit/internal/oob/ider"
	"github.com/spf13/cobra"
)

func bootCmd() *cobra.Command {
	var iso string
	var trace bool
	cmd := &cobra.Command{
		Use:   "boot <mac>",
		Short: "Boot an AMT machine once from an ISO over IDE-R and print what the firmware does with it (bring-up and diagnostics)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mac := strings.ToLower(args[0])
			m, crypto, err := openManagerCrypto()
			if err != nil {
				return err
			}
			_ = crypto
			defer m.Store.Close()
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			row, err := m.Store.GetMachine(ctx, mac)
			if err != nil {
				return err
			}
			cfg, err := m.Store.MachineOOB(ctx, mac)
			if err != nil {
				return fmt.Errorf("%s has no remote management configured", mac)
			}
			if iso == "talos" {
				arch := row.Arch
				if arch == "" {
					arch = "amd64"
				}
				home, _ := homeDir()
				cache := boot.NewCache(home + "/cache")
				schematic, err := m.Factory.CreateSchematic(ctx, []string{"siderolabs/gvisor"})
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), "fetching the Talos ISO")
				if iso, err = cache.TalosISO(ctx, m.Factory.BaseURL, schematic, "v1.14.1", arch); err != nil {
					return err
				}
			}
			media, err := ider.OpenMedia(iso)
			if err != nil {
				return err
			}
			defer media.Close()
			mgr, err := oob.Open(*cfg)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: enabling IDE-R and switching user consent off\n", cfg.Host)
			if err := mgr.PrepareRedirection(ctx); err != nil {
				return err
			}
			events := make(chan ider.Event, 64)
			done := make(chan error, 1)
			go func() {
				done <- ider.Serve(ctx, ider.Config{Host: cfg.Host, User: cfg.User, Password: cfg.Password, TLS: cfg.TLS, Trace: trace}, media, events)
			}()
			opened := false
			start := time.Now()
			for !opened {
				select {
				case e := <-events:
					fmt.Fprintf(cmd.OutOrStdout(), "%6.1fs %s\n", time.Since(start).Seconds(), e.Kind)
					if e.Kind == ider.Opened {
						opened = true
					}
				case err := <-done:
					return fmt.Errorf("session ended: %v", err)
				case <-time.After(60 * time.Second):
					return fmt.Errorf("no session within 60 s")
				}
			}
			fmt.Fprintln(cmd.OutOrStdout(), "booting from the virtual CD")
			if err := mgr.Power(ctx, oob.BootMedia); err != nil {
				return err
			}
			for {
				select {
				case e := <-events:
					switch e.Kind {
					case ider.Progress:
						fmt.Fprintf(cmd.OutOrStdout(), "%6.1fs read %.1f MB\n", time.Since(start).Seconds(), float64(e.Bytes)/1e6)
					default:
						fmt.Fprintf(cmd.OutOrStdout(), "%6.1fs %s %s\n", time.Since(start).Seconds(), e.Kind, e.Reason)
					}
				case err := <-done:
					if err != nil && ctx.Err() == nil {
						return err
					}
					return nil
				}
			}
		},
	}
	cmd.Flags().StringVar(&iso, "iso", "talos", "path to an ISO, or \"talos\" for the Image Factory ISO")
	cmd.Flags().BoolVar(&trace, "trace", false, "print every IDE-R frame")
	return cmd
}
