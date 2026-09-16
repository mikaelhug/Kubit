package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mikael/kubit/internal/factory"
	"github.com/mikael/kubit/internal/pxe"
	"github.com/spf13/cobra"
)

func pxeCmd() *cobra.Command {
	var (
		iface, schematic, talosVersion string
		extensions                     []string
		httpPort                       int
		kubitURL                       string
		httpOnly                       bool
		advertise                      string
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
			if advertise != "" {
				// An interface that appears later (vmnet's bridge100 exists only while
				// a VM runs) can still be advertised in boot lines and preseed URLs.
				if ip = net.ParseIP(advertise); ip == nil {
					return fmt.Errorf("--ip %q is not an IPv4 address", advertise)
				}
			} else if err != nil {
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
				Config:  pxe.Config{Interface: iface, IP: ip, HTTPPort: httpPort, Log: logger, Decide: pxeDecider(kubitURL, os.Getenv("KUBIT_TOKEN"), logger), KubitURL: kubitURL, KubitToken: os.Getenv("KUBIT_TOKEN"), HTTPOnly: httpOnly},
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
	cmd.Flags().StringVar(&advertise, "ip", "", "address to advertise in boot scripts and preseed URLs (default: the interface's IPv4)")
	cmd.Flags().BoolVar(&httpOnly, "http-only", false, "serve only HTTP (boot assets, lab-host preseed and progress) on --http-port; no DHCP/TFTP, no root. For machines you boot yourself")
	cmd.Flags().StringVar(&kubitURL, "kubit-url", "http://127.0.0.1:8080", "daemon to ask whether a MAC should boot Talos or its own disk (cluster members boot locally); KUBIT_TOKEN for its bearer token")
	return cmd
}

// pxeDecider asks the daemon per MAC and caches the answer briefly; when the daemon is
// down every machine gets Talos, as before.
func pxeDecider(url, token string, logger *log.Logger) func(string) string {
	type entry struct {
		boot string
		at   time.Time
	}
	var mu sync.Mutex
	cache := map[string]entry{}
	client := &http.Client{Timeout: 2 * time.Second}
	return func(mac string) string {
		mu.Lock()
		if e, ok := cache[mac]; ok && time.Since(e.at) < 10*time.Second {
			mu.Unlock()
			return e.boot
		}
		mu.Unlock()
		req, err := http.NewRequest("GET", strings.TrimRight(url, "/")+"/api/v1/pxe/decide?mac="+neturl.QueryEscape(mac), nil)
		if err != nil {
			return ""
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			logger.Printf("pxe: daemon unreachable (%v); serving Talos to %s", err, mac)
			return ""
		}
		defer resp.Body.Close()
		var d struct{ Boot, Reason string }
		if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
			return ""
		}
		logger.Printf("pxe: %s → %s (%s)", mac, d.Boot, d.Reason)
		mu.Lock()
		cache[mac] = entry{boot: d.Boot, at: time.Now()}
		mu.Unlock()
		return d.Boot
	}
}
