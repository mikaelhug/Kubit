package config

import (
	"fmt"
	"net/netip"
	"strings"
)

type Network struct {
	PodCIDR     string   `yaml:"podCIDR,omitempty" json:"podCIDR,omitempty"`
	ServiceCIDR string   `yaml:"serviceCIDR,omitempty" json:"serviceCIDR,omitempty"`
	Nameservers []string `yaml:"nameservers,omitempty" json:"nameservers,omitempty"`
	NTP         []string `yaml:"ntp,omitempty" json:"ntp,omitempty"`
}

func (n Node) TargetIP() string {
	if n.Network != nil && len(n.Network.Addresses) > 0 {
		if pfx, err := netip.ParsePrefix(n.Network.Addresses[0]); err == nil {
			return pfx.Addr().String()
		}
		if a, err := netip.ParseAddr(n.Network.Addresses[0]); err == nil {
			return a.String()
		}
	}
	return n.IP
}

type NodeNetwork struct {
	Addresses   []string `yaml:"addresses" json:"addresses"`
	Gateway     string   `yaml:"gateway,omitempty" json:"gateway,omitempty"`
	Nameservers []string `yaml:"nameservers,omitempty" json:"nameservers,omitempty"`
	VLAN        uint16   `yaml:"vlan,omitempty" json:"vlan,omitempty"`
	MTU         uint32   `yaml:"mtu,omitempty" json:"mtu,omitempty"`
}

const (
	RegistryHost = "registry.kubit"
	RegistryPort = 5000
)

const registryIPOffset = 50

func (c *Cluster) RegistryIP() string {
	p, err := netip.ParsePrefix(c.Spec.Network.ServiceCIDR)
	if err != nil || !p.Addr().Is4() || p.Bits() > 22 {
		return ""
	}
	b := p.Masked().Addr().As4()
	b[3] += registryIPOffset
	return netip.AddrFrom4(b).String()
}

func ParseIPRange(s string) (netip.Addr, netip.Addr, error) {
	from, to, ok := strings.Cut(s, "-")
	if !ok {
		return netip.Addr{}, netip.Addr{}, fmt.Errorf("%q is not of the form start-end", s)
	}
	a, err := netip.ParseAddr(strings.TrimSpace(from))
	if err != nil {
		return netip.Addr{}, netip.Addr{}, err
	}
	b, err := netip.ParseAddr(strings.TrimSpace(to))
	if err != nil {
		return netip.Addr{}, netip.Addr{}, err
	}
	if b.Less(a) {
		return netip.Addr{}, netip.Addr{}, fmt.Errorf("%q: end precedes start", s)
	}
	return a, b, nil
}
