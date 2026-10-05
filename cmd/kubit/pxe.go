package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mikael/kubit/internal/config"
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
			logger := log.New(os.Stderr, "", log.LstdFlags)
			srv := &pxe.Server{
				Config:  pxe.Config{Interface: iface, IP: ip, HTTPPort: httpPort, Log: logger, Decide: pxeDecider(kubitURL, os.Getenv("KUBIT_TOKEN"), logger), HTTPOnly: httpOnly},
				Profile: pxe.Profile{SchematicID: schematic, TalosVersion: talosVersion},
				Cache:   pxe.NewCache(cache),
				Factory: f,
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
	cmd.Flags().StringVar(&kubitURL, "kubit-url", "http://127.0.0.1:8090", "daemon that decides Talos or local disk per MAC; KUBIT_TOKEN for its bearer token")
	return cmd
}

func pxeDecider(url, token string, logger *log.Logger) func(string) string {
	cache := newDecideCache(1024)
	client := &http.Client{Timeout: 2 * time.Second}
	return func(mac string) string {
		if boot, ok := cache.get(mac, time.Now()); ok {
			return boot
		}
		req, err := http.NewRequest("GET", strings.TrimRight(url, "/")+"/api/v1/pxe/decide?mac="+neturl.QueryEscape(mac), nil)
		if err != nil {
			return ""
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			logger.Printf("pxe: daemon unreachable (%v); no boot offer for %s, it boots its own disk", err, mac)
			return ""
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			logger.Printf("pxe: daemon answered %s; no boot offer for %s, it boots its own disk", resp.Status, mac)
			return ""
		}
		var d struct{ Boot, Reason string }
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&d); err != nil {
			cache.put(mac, "", time.Now(), decideErrorTTL)
			return ""
		}
		logger.Printf("pxe: %s → %s (%s)", mac, d.Boot, d.Reason)
		cache.put(mac, d.Boot, time.Now(), decideTTL)
		return d.Boot
	}
}

const (
	decideTTL      = 10 * time.Second
	decideErrorTTL = 2 * time.Second
)

type decideCache struct {
	mu      sync.Mutex
	max     int
	entries map[string]decideEntry
}

type decideEntry struct {
	boot    string
	expires time.Time
}

func newDecideCache(max int) *decideCache {
	return &decideCache{max: max, entries: map[string]decideEntry{}}
}

func (c *decideCache) get(mac string, now time.Time) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[mac]
	if !ok || now.After(e.expires) {
		return "", false
	}
	return e.boot, true
}

func (c *decideCache) put(mac, boot string, now time.Time, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entries[mac]; !ok && len(c.entries) >= c.max {
		for k, e := range c.entries {
			if now.After(e.expires) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= c.max {
			clear(c.entries)
		}
	}
	c.entries[mac] = decideEntry{boot: boot, expires: now.Add(ttl)}
}
