package talos

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

type NodeState string

const (
	StateMaintenance NodeState = "maintenance"
	StateConfigured  NodeState = "configured"
)

type ScanResult struct {
	IP        string
	State     NodeState
	Inventory *Inventory
	Err       error
}

func ExpandTargets(targets []string) ([]netip.Addr, error) {
	var out []netip.Addr
	for _, t := range targets {
		t = strings.TrimSpace(t)
		if strings.Contains(t, "/") {
			p, err := netip.ParsePrefix(t)
			if err != nil {
				return nil, err
			}
			p = p.Masked()
			if p.Addr().Is4() && p.Bits() > 30 {
				return nil, fmt.Errorf("%s: prefix too small to scan", t)
			}
			if p.Addr().BitLen()-p.Bits() > 16 {
				return nil, fmt.Errorf("%s: prefix too large to scan", t)
			}
			first := p.Addr().Next()
			for a := first; p.Contains(a); a = a.Next() {
				if next := a.Next(); !p.Contains(next) && p.Addr().Is4() {
					break
				}
				out = append(out, a)
			}
			continue
		}
		a, err := netip.ParseAddr(t)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

func Scan(ctx context.Context, addrs []netip.Addr, concurrency int, timeout time.Duration) []ScanResult {
	if concurrency <= 0 {
		concurrency = 64
	}
	sem := make(chan struct{}, concurrency)
	var (
		mu      sync.Mutex
		results []ScanResult
		wg      sync.WaitGroup
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
			if !PortOpen(ctx, ip, timeout) {
				return
			}
			r := Probe(ctx, ip, timeout)
			mu.Lock()
			results = append(results, r)
			mu.Unlock()
		}(a.String())
	}
	wg.Wait()
	sort.Slice(results, func(i, j int) bool {
		return netip.MustParseAddr(results[i].IP).Less(netip.MustParseAddr(results[j].IP))
	})
	return results
}

func Probe(ctx context.Context, ip string, timeout time.Duration) ScanResult {
	ctx, cancel := context.WithTimeout(ctx, 3*timeout)
	defer cancel()
	c, err := DialMaintenance(ctx, ip)
	if err != nil {
		return ScanResult{IP: ip, Err: err}
	}
	defer c.Close()
	inv, err := c.Inspect(ctx)
	if err != nil {
		if isTLSRejection(err) {
			return ScanResult{IP: ip, State: StateConfigured}
		}
		return ScanResult{IP: ip, Err: err}
	}
	if inv.Stage != "maintenance" {
		return ScanResult{IP: ip, State: StateConfigured, Inventory: inv}
	}
	return ScanResult{IP: ip, State: StateMaintenance, Inventory: inv}
}

func isTLSRejection(err error) bool {
	s := err.Error()
	return strings.Contains(s, "certificate required") ||
		strings.Contains(s, "bad certificate") ||
		strings.Contains(s, "remote error: tls:")
}

var (
	silent  = []string{"host is down", "no route to host", "i/o timeout", "operation timed out", "deadline exceeded", "network is unreachable"}
	refused = "connection refused"
)

func Describe(err error) string {
	if err == nil {
		return "does not answer"
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, refused):
		return "answers but runs no Talos API"
	case slices.ContainsFunc(silent, func(s string) bool { return strings.Contains(msg, s) }):
		return "does not answer"
	}
	return msg
}

func FindMember(ctx context.Context, lan netip.Prefix, talosconfig []byte, hostname string, skip ...string) (string, error) {
	addrs, err := ExpandTargets([]string{lan.String()})
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sem := make(chan struct{}, 64)
	found := make(chan string, 1)
	var wg sync.WaitGroup
	for _, a := range addrs {
		ip := a.String()
		if slices.Contains(skip, ip) || ctx.Err() != nil {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if !PortOpen(ctx, ip, 2*time.Second) {
				return
			}
			call, done := context.WithTimeout(ctx, 5*time.Second)
			defer done()
			tc, err := Dial(call, ip, talosconfig)
			if err != nil {
				return
			}
			defer tc.Close()
			if h, err := tc.Hostname(call); err == nil && h == hostname {
				select {
				case found <- ip:
					cancel()
				default:
				}
			}
		}()
	}
	wg.Wait()
	select {
	case ip := <-found:
		return ip, nil
	default:
		return "", fmt.Errorf("%s is not on %s", hostname, lan)
	}
}
