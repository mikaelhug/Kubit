package cluster

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/mikaelhug/kubit/internal/config"
)

const ActRejoin = "rejoin"

type liveMachine struct {
	hostname string
	role     config.Role
	addr     string
}

type rejoin struct {
	node config.Node
	from liveMachine
}

type handover struct {
	from, to string
}

func (p *Plan) handsOver(host string) bool { return p.handover != nil && p.handover.from == host }

func (p *Plan) takesOver(host string) bool { return p.handover != nil && p.handover.to == host }

func (p *Plan) rejoinSource(host string) bool {
	return slices.ContainsFunc(p.rejoins, func(r rejoin) bool { return r.from.hostname == host })
}

func (p *Plan) resets() []string {
	var out []string
	for _, r := range p.rejoins {
		out = append(out, r.from.hostname)
	}
	return append(out, p.removes...)
}

func endpointHolder(c *config.Cluster) string {
	if c.Spec.ControlPlane.VIP != "" {
		return ""
	}
	host := config.EndpointHost(c.Spec.ControlPlane.Endpoint)
	for _, n := range c.ControlPlanes() {
		if n.IP == host {
			return n.Hostname
		}
	}
	return ""
}

func liveEndpointHolder(c *config.Cluster, ls *liveState) string {
	if c.Spec.ControlPlane.VIP != "" {
		return ""
	}
	host := config.EndpointHost(c.Spec.ControlPlane.Endpoint)
	for _, h := range sortedKeys(ls.kube) {
		if kn := ls.kube[h]; kn.InternalIP == host && undeclaredNode(h, kn).Role == config.RoleControlPlane {
			return h
		}
	}
	return ""
}

func planHandover(c *config.Cluster, ls *liveState) (*handover, []string) {
	from := liveEndpointHolder(c, ls)
	if from == "" || c.NodeIndex(from) >= 0 {
		return nil, nil
	}
	host := config.EndpointHost(c.Spec.ControlPlane.Endpoint)
	var to []string
	for _, n := range c.ControlPlanes() {
		ln := ls.nodes[n.Hostname]
		if n.Network == nil || n.TargetIP() != host || ln == nil || (ln.member && ln.addr == host) {
			continue
		}
		if ln.member || ln.maint || ln.rejoin != nil {
			to = append(to, n.Hostname)
		}
	}
	switch {
	case len(to) == 0:
		return nil, nil
	case len(to) > 1:
		return nil, []string{fmt.Sprintf("%s all take over %s from %s; declare one", strings.Join(to, ", "), host, from)}
	}
	n := c.Spec.Nodes[c.NodeIndex(to[0])]
	if n.IP == host {
		return nil, []string{fmt.Sprintf("%s: set ip: to its current address; it takes over %s once %s is gone", n.Hostname, host, from)}
	}
	return &handover{from: from, to: to[0]}, nil
}

func interim(n config.Node) config.Node {
	if n.Network == nil || len(n.Network.Addresses) == 0 {
		return n
	}
	bits := 24
	if pfx, err := netip.ParsePrefix(n.Network.Addresses[0]); err == nil {
		bits = pfx.Bits()
	}
	nn := *n.Network
	nn.Addresses = append([]string{netip.PrefixFrom(netip.MustParseAddr(n.IP), bits).String()}, n.Network.Addresses[1:]...)
	n.Network = &nn
	return n
}

func (p *Plan) planRejoin(n config.Node, ln *liveNode, opts ConvergeOptions) {
	node := n
	if p.takesOver(n.Hostname) {
		node = interim(n)
	}
	node.IP = ln.rejoin.addr
	p.rejoins = append(p.rejoins, rejoin{node: node, from: *ln.rejoin})
	p.install(node)
	ch := Change{Action: ActRejoin, Target: n.Hostname, Detail: fmt.Sprintf("%s %s → %s at %s", ln.rejoin.hostname, ln.rejoin.role, n.Role, ln.rejoin.addr)}
	if ln.rejoin.hostname == n.Hostname {
		ch.Detail = fmt.Sprintf("%s → %s at %s", ln.rejoin.role, n.Role, ln.rejoin.addr)
	}
	if !opts.AllowRemoval {
		ch.Blocked = "needs --allow-removal"
	}
	p.Changes = append(p.Changes, ch)
}

func (p *Plan) planTakeover(c *config.Cluster, ls *liveState) {
	if p.handover == nil {
		return
	}
	n := c.Spec.Nodes[c.NodeIndex(p.handover.to)]
	if ls.nodes[n.Hostname].member {
		return
	}
	ch := addrChange{node: n, from: n.IP, to: n.TargetIP(), kind: addrMove, after: true}
	p.moves = append(p.moves, ch)
	p.Changes = append(p.Changes, Change{Action: ActAddress, Target: n.Hostname, Detail: ch.describe()})
}

func (m *Manager) rejoin(ctx context.Context, d *Desired, r rejoin, sink Sink) error {
	name := d.Cluster.Metadata.Name
	if err := m.removeNode(ctx, name, r.from.hostname, "", sink); err != nil {
		return err
	}
	probe := config.Node{Hostname: r.node.Hostname, IP: r.from.addr, MAC: r.node.MAC}
	var found map[string]string
	if err := sink.Run("maintenance", func() error {
		var err error
		found, err = waitMaintenance(ctx, []config.Node{probe}, nil, sink)
		return err
	}); err != nil {
		return err
	}
	n := r.node
	n.IP = found[n.Hostname]
	return m.AddNode(ctx, name, n, sink)
}
