package pxe

import (
	"fmt"
	"net"
	"sync"
	"time"
)

const addrTTL = 5 * time.Second

type ifaceAddr struct {
	name   string
	lookup func(string) (net.IP, error)
	logf   func(format string, args ...any)
	mu     sync.Mutex
	ip     net.IP
	at     time.Time
}

func (a *ifaceAddr) get(now time.Time) net.IP {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.at.IsZero() && now.Sub(a.at) < addrTTL {
		return a.ip
	}
	ip, err := a.lookup(a.name)
	if err != nil {
		ip = nil
	}
	if !ip.Equal(a.ip) || a.at.IsZero() {
		switch {
		case ip != nil:
			a.logf("pxe: %s has %s", a.name, ip)
		case err != nil:
			a.logf("pxe: %v; not answering until it has one", err)
		}
	}
	a.ip, a.at = ip, now
	return ip
}

func InterfaceIPv4(name string) (net.IP, error) {
	ifc, err := net.InterfaceByName(name)
	if err != nil {
		return nil, err
	}
	addrs, err := ifc.Addrs()
	if err != nil {
		return nil, err
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
			return ipn.IP.To4(), nil
		}
	}
	return nil, fmt.Errorf("%s has no IPv4 address", name)
}
