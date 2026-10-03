package config

import (
	"net/netip"
	"slices"

	"github.com/siderolabs/talos/pkg/machinery/config/config"
	"github.com/siderolabs/talos/pkg/machinery/config/types/network"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	"github.com/siderolabs/talos/pkg/machinery/nethelpers"
)

const (
	metalLBMemberlistPort = 7946
	nodePortLo            = 30000
	nodePortHi            = 32767
)

var anywhere = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}

func (c *Cluster) FirewallOn() bool {
	return c.Spec.Network.Firewall != nil && *c.Spec.Network.Firewall
}

func (c *Cluster) PoliciesOn() bool {
	return c.Spec.Network.Policies == nil || *c.Spec.Network.Policies
}

func (c *Cluster) DiscoveryOn() bool {
	return c.Spec.Network.Discovery == nil || *c.Spec.Network.Discovery
}

func ClusterSubnets(c *Cluster) []netip.Prefix {
	var out []netip.Prefix
	for _, n := range c.Spec.Nodes {
		out = append(out, nodeSubnets(n)...)
	}
	slices.SortFunc(out, comparePrefix)
	return slices.Compact(out)
}

func nodeSubnets(n Node) []netip.Prefix {
	var out []netip.Prefix
	if n.Network != nil {
		for _, a := range n.Network.Addresses {
			if pfx, err := netip.ParsePrefix(a); err == nil {
				out = append(out, pfx.Masked())
			}
		}
	}
	if len(out) > 0 {
		return out
	}
	if lan, ok := slash24(n.IP); ok {
		return []netip.Prefix{lan}
	}
	if a, err := netip.ParseAddr(n.IP); err == nil {
		return []netip.Prefix{netip.PrefixFrom(a, a.BitLen())}
	}
	return nil
}

func comparePrefix(a, b netip.Prefix) int {
	if c := a.Addr().Compare(b.Addr()); c != 0 {
		return c
	}
	return a.Bits() - b.Bits()
}

func firewallDocs(c *Cluster, n Node) []config.Document {
	if !c.FirewallOn() {
		return nil
	}
	subnets := ClusterSubnets(c)
	var pods []netip.Prefix
	if pfx, err := netip.ParsePrefix(c.Spec.Network.PodCIDR); err == nil {
		pods = []netip.Prefix{pfx.Masked()}
	}
	cp := n.Role == RoleControlPlane
	tcp, udp := nethelpers.ProtocolTCP, nethelpers.ProtocolUDP
	block := network.NewDefaultActionConfigV1Alpha1()
	block.Ingress = nethelpers.DefaultActionBlock
	docs := []config.Document{
		block,
		firewallRule("apid", tcp, anywhere, constants.ApidPort),
		firewallRule("kubelet", tcp, slices.Concat(subnets, pods), constants.KubeletPort),
		firewallRule("flannel-vxlan", udp, subnets, constants.FlannelDefaultBackendPort),
	}
	if cp {
		docs = append(docs,
			firewallRule("kubernetes-api", tcp, anywhere, constants.DefaultControlPlanePort),
			firewallRule("trustd", tcp, subnets, constants.TrustdPort),
			firewallRule("etcd", tcp, subnets, constants.EtcdClientPort, constants.EtcdPeerPort, constants.EtcdClientHTTPPort),
		)
	}
	if c.Spec.Platform.MetalLB.Enabled {
		docs = append(docs,
			firewallRule("metallb-tcp", tcp, subnets, metalLBMemberlistPort),
			firewallRule("metallb-udp", udp, subnets, metalLBMemberlistPort),
		)
	} else if c.Spec.Platform.Traefik.Enabled {
		nodePorts := firewallRule("ingress-nodeports", tcp, anywhere)
		nodePorts.PortSelector.Ports = network.PortRanges{{Lo: nodePortLo, Hi: nodePortHi}}
		docs = append(docs, nodePorts)
	}
	return docs
}

func firewallRule(name string, proto nethelpers.Protocol, from []netip.Prefix, ports ...int) *network.RuleConfigV1Alpha1 {
	r := network.NewRuleConfigV1Alpha1()
	r.MetaName = name
	r.PortSelector.Protocol = proto
	for _, p := range ports {
		port := uint16(p)
		if last := len(r.PortSelector.Ports) - 1; last >= 0 && r.PortSelector.Ports[last].Hi+1 == port {
			r.PortSelector.Ports[last].Hi = port
			continue
		}
		r.PortSelector.Ports = append(r.PortSelector.Ports, network.PortRange{Lo: port, Hi: port})
	}
	for _, s := range from {
		r.Ingress = append(r.Ingress, network.IngressRule{Subnet: s})
	}
	return r
}
