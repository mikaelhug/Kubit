package talos

import (
	"context"
	"fmt"
	"net/netip"
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
		strings.Contains(s, "tls:") ||
		strings.Contains(s, "authentication handshake failed")
}
