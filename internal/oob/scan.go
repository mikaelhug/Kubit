package oob

import (
	"context"
	"net"
	"net/netip"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// ScanResult is one address that answers on the AMT port. Info is filled when
// credentials were available; MAC falls back to the ARP table (same segment only).
type ScanResult struct {
	IP   string
	MAC  string
	Info *Info
	Err  error // credentials wrong/absent: the machine is still recorded
}

// Scan probes the AMT port on every address and, with credentials, asks each engine
// who it is. Addresses that run Talos are excluded by the caller.
func Scan(ctx context.Context, addrs []netip.Addr, creds Config, timeout time.Duration) []ScanResult {
	sem := make(chan struct{}, 64)
	var (
		mu  sync.Mutex
		out []ScanResult
		wg  sync.WaitGroup
	)
	for _, a := range addrs {
		wg.Add(1)
		go func(ip string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			d := net.Dialer{Timeout: timeout}
			conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, "16992"))
			if err != nil {
				return
			}
			conn.Close()
			r := ScanResult{IP: ip, MAC: macFromARP(ip)}
			if creds.User != "" && creds.Password != "" {
				c := creds
				c.Type, c.Host = "amt", ip
				if m, err := Open(c); err == nil {
					pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
					info, err := m.Probe(pctx)
					cancel()
					if err != nil {
						r.Err = err
					} else {
						r.Info = &info
						if info.MAC != "" {
							r.MAC = info.MAC
						}
					}
				}
			}
			mu.Lock()
			out = append(out, r)
			mu.Unlock()
		}(a.String())
	}
	wg.Wait()
	return out
}

var macRe = regexp.MustCompile(`([0-9a-fA-F]{1,2}[:-]){5}[0-9a-fA-F]{1,2}`)

// macFromARP reads the neighbour table after the TCP probe populated it; only works
// for addresses on the daemon host's own segment, which is where PXE works anyway.
func macFromARP(ip string) string {
	var out []byte
	var err error
	if runtime.GOOS == "linux" {
		out, err = exec.Command("ip", "neigh", "show", ip).Output()
	} else {
		out, err = exec.Command("arp", "-n", ip).Output()
	}
	if err != nil {
		return ""
	}
	m := macRe.FindString(string(out))
	if m == "" {
		return ""
	}
	// macOS prints single-digit octets ("4:e:3c:..."); normalise to two digits.
	parts := strings.Split(strings.ReplaceAll(m, "-", ":"), ":")
	for i, p := range parts {
		if len(p) == 1 {
			parts[i] = "0" + p
		}
	}
	return strings.ToLower(strings.Join(parts, ":"))
}
