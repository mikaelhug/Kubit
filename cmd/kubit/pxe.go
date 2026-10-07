package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/mikaelhug/kubit/internal/config"
	"github.com/mikaelhug/kubit/internal/factory"
	"github.com/mikaelhug/kubit/internal/pxe"
	"github.com/mikaelhug/kubit/internal/repo"
	"github.com/spf13/cobra"
)

func pxeCmd() *cobra.Command {
	var (
		iface, schematic, talosVersion string
		extensions                     []string
		httpPort                       int
		httpOnly                       bool
		advertise                      string
		closed                         bool
	)
	cmd := &cobra.Command{
		Use:   "pxe [dirs...]",
		Short: "Network-boot machines into Talos maintenance mode; nodes declared in the repos boot their own disk (run with sudo)",
		Long: `Answers PXE firmware next to the LAN's real DHCP server (which keeps assigning
addresses), serves iPXE over TFTP and an iPXE script over HTTP that boots the Talos
kernel and initramfs for the given schematic, cached from the Image Factory. Booted
machines land in maintenance mode and show up in the console's Discovery.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, repos []string) error {
			var ip net.IP
			if advertise != "" {
				if ip = net.ParseIP(advertise).To4(); ip == nil {
					return fmt.Errorf("--ip %q is not an IPv4 address", advertise)
				}
			} else if _, err := net.InterfaceByName(iface); err != nil {
				return fmt.Errorf("--iface %s: %w", iface, err)
			}
			f := factory.New()
			if schematic == "" {
				var err error
				if schematic, err = f.CreateSchematic(cmd.Context(), extensions); err != nil {
					return err
				}
			}
			home, err := homeDir()
			if err != nil {
				return err
			}
			cache := filepath.Join(home, "cache")
			if err := ensureDir(home, home, 0o700); err != nil {
				return err
			}
			if err := ensureDir(home, cache, 0o700); err != nil {
				return err
			}
			run := filepath.Join(home, "run")
			if err := ensureDir(home, run, 0o755); err != nil {
				return err
			}
			logger := log.New(os.Stderr, "", log.LstdFlags)
			decide := repoDecider(repos, closed, logger)
			srv := &pxe.Server{
				Config:  pxe.Config{Interface: iface, IP: ip, HTTPPort: httpPort, Log: logger, Decide: decide, HTTPOnly: httpOnly},
				Profile: pxe.Profile{SchematicID: schematic, TalosVersion: talosVersion},
				Cache:   pxe.NewCache(cache),
				Factory: f,
				RunFile: filepath.Join(run, "pxe"),
			}
			return srv.Run(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&iface, "iface", "en0", "LAN interface to answer on")
	cmd.Flags().StringVar(&schematic, "schematic", "", "Image Factory schematic ID (default: create from --extensions)")
	cmd.Flags().StringSliceVar(&extensions, "extensions", nil, "system extensions for the default schematic")
	cmd.Flags().StringVar(&talosVersion, "talos-version", config.MinTalosVersion, "Talos release to boot")
	cmd.Flags().IntVar(&httpPort, "http-port", 8069, "port for the iPXE script and boot assets")
	cmd.Flags().StringVar(&advertise, "ip", "", "address to advertise in boot scripts (default: the interface's current IPv4)")
	cmd.Flags().BoolVar(&httpOnly, "http-only", false, "serve only the iPXE script and boot assets on --http-port; no DHCP/TFTP, no root")
	cmd.Flags().BoolVar(&closed, "closed", false, "machines not declared in any repo get no Talos")
	return cmd
}

func repoDecider(dirs []string, closed bool, logger *log.Logger) func(string) string {
	return func(mac string) string {
		for _, dir := range dirs {
			c, _, err := repo.LoadSpec(dir)
			if err != nil {
				logger.Printf("pxe: %v; no boot offer for %s", err, mac)
				return ""
			}
			for _, n := range c.Spec.Nodes {
				if strings.EqualFold(n.MAC, mac) {
					logger.Printf("pxe: %s → local (%s in %s)", mac, n.Hostname, c.Metadata.Name)
					return "local"
				}
			}
		}
		if closed {
			logger.Printf("pxe: %s → local (not declared, enrollment closed)", mac)
			return "local"
		}
		logger.Printf("pxe: %s → talos (not declared)", mac)
		return "talos"
	}
}
