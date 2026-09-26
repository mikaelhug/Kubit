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

const AMTPort = "16992"

type ScanResult struct {
	IP   string
	Type string
	MAC  string
	Info *Info
	Err  error
}

func Scan(ctx context.Context, addrs []netip.Addr, amtCreds, bmcCreds Config, timeout time.Duration) []ScanResult {
	sem := make(chan struct{}, 64)
	var (
		mu  sync.Mutex
		out []ScanResult
		wg  sync.WaitGroup
	)
	for _, a := range addrs {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(ip string) {
			defer wg.Done()
			defer func() { <-sem }()
			d := net.Dialer{Timeout: timeout}
			conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, AMTPort))
			var r ScanResult
			var creds Config
			if err == nil {
				conn.Close()
				r = ScanResult{IP: ip, Type: "amt", MAC: macFromARP(ctx, ip)}
				creds = amtCreds
			} else if ProbeRedfish(ctx, ip, timeout) {
				r = ScanResult{IP: ip, Type: "redfish"}
				creds = bmcCreds
			} else {
				return
			}
			if creds.User != "" && creds.Password != "" {
				c := creds
				c.Type, c.Host = r.Type, ip
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

func macFromARP(ctx context.Context, ip string) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var out []byte
	var err error
	if runtime.GOOS == "linux" {
		out, err = exec.CommandContext(ctx, "ip", "neigh", "show", ip).Output()
	} else {
		out, err = exec.CommandContext(ctx, "arp", "-n", ip).Output()
	}
	if err != nil {
		return ""
	}
	m := macRe.FindString(string(out))
	if m == "" {
		return ""
	}
	parts := strings.Split(strings.ReplaceAll(m, "-", ":"), ":")
	for i, p := range parts {
		if len(p) == 1 {
			parts[i] = "0" + p
		}
	}
	return strings.ToLower(strings.Join(parts, ":"))
}
