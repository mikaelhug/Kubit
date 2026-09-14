package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/mikael/kubit/internal/factory"
	"github.com/mikael/kubit/internal/pxe"
	"github.com/spf13/cobra"
)

func pxeCmd() *cobra.Command {
	var (
		iface, schematic, talosVersion string
		extensions                     []string
		httpPort                       int
	)
	cmd := &cobra.Command{
		Use:   "pxe",
		Short: "Network-boot machines on the LAN into Talos maintenance mode (proxyDHCP + TFTP + HTTP; run with sudo)",
		Long: `Answers PXE firmware next to the LAN's real DHCP server (which keeps assigning
addresses), serves iPXE over TFTP and an iPXE script over HTTP that boots the Talos
kernel and initramfs for the given schematic, cached from the Image Factory. Booted
machines land in maintenance mode and show up in 'kubit discover'.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ip, err := pxe.InterfaceIPv4(iface)
			if err != nil {
				return err
			}
			f := factory.New()
			if schematic == "" {
				if schematic, err = f.CreateSchematic(cmd.Context(), extensions); err != nil {
					return err
				}
			}
			home, err := homeDir()
			if err != nil {
				return err
			}
			logger := log.New(os.Stderr, "", log.LstdFlags)
			srv := &pxe.Server{
				Config:  pxe.Config{Interface: iface, IP: ip, HTTPPort: httpPort, Log: logger},
				Profile: pxe.Profile{SchematicID: schematic, TalosVersion: talosVersion},
				Cache:   pxe.NewCache(filepath.Join(home, "cache")),
				Factory: f,
			}
			return srv.Run(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&iface, "iface", "en0", "LAN interface to answer on")
	cmd.Flags().StringVar(&schematic, "schematic", "", "Image Factory schematic ID (default: create from --extensions)")
	cmd.Flags().StringSliceVar(&extensions, "extensions", []string{"siderolabs/gvisor"}, "system extensions for the default schematic")
	cmd.Flags().StringVar(&talosVersion, "talos-version", "v1.14.0", "Talos release to boot")
	cmd.Flags().IntVar(&httpPort, "http-port", 8069, "port for the iPXE script and boot assets")
	return cmd
}
