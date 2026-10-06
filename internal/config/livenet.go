package config

import (
	"slices"
	"strconv"
	"strings"

	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/types/network"
)

type Addressing struct {
	DHCP      bool     `json:"dhcp"`
	Addresses []string `json:"addresses,omitempty"`
	Gateway   string   `json:"gateway,omitempty"`
	VLAN      uint16   `json:"vlan,omitempty"`
	MTU       uint32   `json:"mtu,omitempty"`
}

func (n Node) Addressing() Addressing {
	if n.Network == nil {
		return Addressing{DHCP: true}
	}
	return Addressing{Addresses: slices.Clone(n.Network.Addresses), Gateway: n.Network.Gateway, VLAN: n.Network.VLAN, MTU: n.Network.MTU}
}

func (a Addressing) Equal(b Addressing) bool {
	return a.DHCP == b.DHCP && slices.Equal(a.Addresses, b.Addresses) && a.Gateway == b.Gateway && a.VLAN == b.VLAN && a.MTU == b.MTU
}

func (a Addressing) String() string {
	if a.DHCP {
		return "DHCP"
	}
	s := strings.Join(a.Addresses, ", ")
	if a.Gateway != "" {
		s += " via " + a.Gateway
	}
	if a.VLAN > 0 {
		s += " on VLAN " + strconv.Itoa(int(a.VLAN))
	}
	return s
}

type LiveConfig struct {
	Addressing Addressing
	Endpoint   string
	Found      bool
}

func ReadLive(cfg []byte) (LiveConfig, error) {
	p, err := configloader.NewFromBytes(cfg)
	if err != nil {
		return LiveConfig{}, err
	}
	var live LiveConfig
	if k := p.K8sClusterConfig(); k != nil && k.ClusterEndpoint() != nil {
		live.Endpoint = k.ClusterEndpoint().String()
	}
	fill := func(c network.CommonLinkConfig, vlan uint16) {
		a := Addressing{VLAN: vlan, MTU: c.LinkMTU}
		for _, addr := range c.LinkAddresses {
			a.Addresses = append(a.Addresses, addr.AddressAddress.String())
		}
		for _, r := range c.LinkRoutes {
			if r.RouteGateway.IsValid() && !r.RouteDestination.IsValid() {
				a.Gateway = r.RouteGateway.String()
			}
		}
		live.Addressing, live.Found = a, true
	}
	for _, d := range p.Documents() {
		switch doc := d.(type) {
		case *network.DHCPv4ConfigV1Alpha1:
			if doc.MetaName == vipLinkAlias && !live.Found {
				live.Addressing, live.Found = Addressing{DHCP: true}, true
			}
		case *network.LinkConfigV1Alpha1:
			if doc.MetaName == vipLinkAlias {
				fill(doc.CommonLinkConfig, 0)
			}
		case *network.VLANConfigV1Alpha1:
			if doc.ParentLinkConfig == vipLinkAlias {
				fill(doc.CommonLinkConfig, doc.VLANIDConfig)
			}
		}
	}
	return live, nil
}
