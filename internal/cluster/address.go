package cluster

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/k8s"
	"github.com/mikael/kubit/internal/netx"
	"github.com/mikael/kubit/internal/repo"
	"github.com/mikael/kubit/internal/talos"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type addrKind string

const (
	addrPin     addrKind = "pin"
	addrMove    addrKind = "move"
	addrRelease addrKind = "release"
	addrRetune  addrKind = "retune"
	addrRecord  addrKind = "record"
	addrRepair  addrKind = "repair"

	tryRollback  = 3 * time.Minute
	findTimeout  = 2 * time.Minute
	addressProbe = 1500 * time.Millisecond
)

var addressInUse = netx.InUse

type addrChange struct {
	node   config.Node
	from   string
	to     string
	kind   addrKind
	live   config.Addressing
	peerID uint64
	stale  string
}

func takenTargets(ctx context.Context, nodes []config.Node, ls *liveState) []string {
	problems := make([]string, len(nodes))
	var wg sync.WaitGroup
	for i, n := range nodes {
		target := n.TargetIP()
		if ln := ls.nodes[n.Hostname]; target == n.IP || (ln != nil && target == ln.addr) {
			continue
		}
		wg.Go(func() {
			if addressInUse(ctx, target, addressProbe) {
				problems[i] = fmt.Sprintf("%s: %s already answers on the network", n.Hostname, target)
			}
		})
	}
	wg.Wait()
	return slices.DeleteFunc(problems, func(p string) bool { return p == "" })
}

func (ch addrChange) moves() bool {
	return ch.kind == addrPin || ch.kind == addrMove || ch.kind == addrRelease || ch.kind == addrRetune
}

func (ch addrChange) describe() string {
	switch ch.kind {
	case addrPin:
		return ch.from + ": DHCP → static"
	case addrMove:
		return ch.from + " → " + ch.to
	case addrRelease:
		return ch.from + " → DHCP"
	case addrRetune:
		return ch.from + ": " + ch.live.String() + " → " + ch.node.Addressing().String()
	case addrRecord:
		return "answers at " + ch.to + "; ip: in cluster.yaml becomes " + ch.to
	case addrRepair:
		return "etcd peer URL " + ch.stale + " → " + peerURL(ch.from)
	}
	return ""
}

func (m *Manager) planAddresses(ctx context.Context, p *Plan, d *Desired, ls *liveState) {
	c := d.Cluster
	declaredHost := config.EndpointHost(c.Spec.ControlPlane.Endpoint)
	liveCPs := len(p.applied.ControlPlanes())
	for _, n := range c.Spec.Nodes {
		ln := ls.nodes[n.Hostname]
		if !ln.member || !ln.live.Found {
			continue
		}
		declared := n.Addressing()
		ch := addrChange{node: n, from: ln.addr, to: n.TargetIP(), live: ln.live.Addressing}
		switch {
		case ln.live.Addressing.Equal(declared):
			if ln.addr == n.IP {
				continue
			}
			ch.kind, ch.to = addrRecord, ln.addr
		case declared.DHCP:
			ch.kind, ch.to = addrRelease, ""
		case ln.addr != n.TargetIP():
			ch.kind = addrMove
		case ln.live.Addressing.DHCP:
			ch.kind = addrPin
		default:
			ch.kind = addrRetune
		}
		p.moves = append(p.moves, ch)
		p.Changes = append(p.Changes, Change{Action: ActAddress, Target: n.Hostname, Detail: ch.describe()})
		cp := n.Role == config.RoleControlPlane
		switch {
		case ch.kind == addrMove && addressInUse(ctx, ch.to, addressProbe):
			p.Problems = append(p.Problems, fmt.Sprintf("%s: %s already answers on the network", n.Hostname, ch.to))
		case cp && (ch.kind == addrMove || ch.kind == addrRelease) && liveCPs == 2:
			p.Problems = append(p.Problems, fmt.Sprintf("%s: with two control planes, etcd loses quorum while one moves; add a third first", n.Hostname))
		case ch.kind == addrRelease && cp && c.Spec.ControlPlane.VIP == "" && config.EndpointHost(ls.endpoint) == ch.from:
			p.Problems = append(p.Problems, fmt.Sprintf("%s holds the API endpoint without a VIP; keep it static or add a VIP before releasing it to DHCP", n.Hostname))
		}
	}
	if ls.endpoint != "" && ls.endpoint != c.Spec.ControlPlane.Endpoint {
		p.Changes = append(p.Changes, Change{Action: ActEndpoint, Detail: ls.endpoint + " → " + c.Spec.ControlPlane.Endpoint})
		if !slices.ContainsFunc(p.moves, func(ch addrChange) bool { return ch.kind == addrMove && ch.to == declaredHost }) {
			p.Problems = append(p.Problems, "controlPlane.endpoint changes without a control plane moving to "+declaredHost+"; move a control plane there or revert the endpoint")
		}
	}
	if !slices.ContainsFunc(p.moves, func(ch addrChange) bool { return ch.moves() && ch.node.Role == config.RoleControlPlane }) {
		m.planPeerRepairs(ctx, p, d, ls)
	}
}

func (m *Manager) planPeerRepairs(ctx context.Context, p *Plan, d *Desired, ls *liveState) {
	var from string
	for _, n := range d.Cluster.ControlPlanes() {
		if ln := ls.nodes[n.Hostname]; ln.member {
			from = ln.addr
			break
		}
	}
	if from == "" {
		return
	}
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tc, err := talos.Dial(call, from, d.Talosconfig)
	if err != nil {
		return
	}
	defer tc.Close()
	peers, err := tc.EtcdPeers(call)
	if err != nil {
		return
	}
	for _, peer := range peers {
		i := d.Cluster.NodeIndex(peer.Hostname)
		if i < 0 {
			continue
		}
		ln := ls.nodes[peer.Hostname]
		if !ln.member || slices.Contains(peer.PeerURLs, peerURL(ln.addr)) || len(peer.PeerURLs) == 0 {
			continue
		}
		ch := addrChange{node: d.Cluster.Spec.Nodes[i], from: ln.addr, kind: addrRepair, peerID: peer.ID, stale: peer.PeerURLs[0]}
		p.moves = append(p.moves, ch)
		p.Changes = append(p.Changes, Change{Action: ActAddress, Target: peer.Hostname, Detail: ch.describe()})
	}
}

func (p *Plan) moving(hostname string) bool {
	return slices.ContainsFunc(p.moves, func(ch addrChange) bool { return ch.moves() && ch.node.Hostname == hostname })
}

func (m *Manager) ApplyAddresses(ctx context.Context, d *Desired, p *Plan, sink Sink) error {
	if len(p.moves) == 0 {
		return nil
	}
	name := d.Cluster.Metadata.Name
	if slices.ContainsFunc(p.moves, func(ch addrChange) bool { return ch.moves() && ch.node.Role == config.RoleControlPlane }) {
		if err := sink.Run("address", func() error { return m.safetySnapshot(ctx, name, "pre-move", sink) }); err != nil {
			return err
		}
	}
	for _, ch := range addressOrder(p.moves, config.EndpointHost(d.Cluster.Spec.ControlPlane.Endpoint)) {
		step := "address:" + ch.node.Hostname
		if err := sink.Run(step, func() error { return m.applyAddress(ctx, d, ch, step, sink) }); err != nil {
			return fmt.Errorf("%s: %w", ch.node.Hostname, err)
		}
	}
	return nil
}

func addressOrder(moves []addrChange, endpoint string) []addrChange {
	rank := func(ch addrChange) int {
		switch {
		case ch.node.Role != config.RoleControlPlane:
			return 2
		case ch.to == endpoint || ch.from == endpoint:
			return 1
		}
		return 0
	}
	order := slices.Clone(moves)
	slices.SortStableFunc(order, func(a, b addrChange) int { return rank(a) - rank(b) })
	return order
}

func (m *Manager) applyAddress(ctx context.Context, d *Desired, ch addrChange, step string, sink Sink) error {
	name, host := d.Cluster.Metadata.Name, ch.node.Hostname
	switch ch.kind {
	case addrRecord:
		return m.recordAddress(d, ch.node, ch.to, step, sink)
	case addrRepair:
		return m.repairEtcdPeer(ctx, d, host, ch.from, step, sink)
	}
	c, _, err := m.LoadCluster(name)
	if err != nil {
		return err
	}
	i := c.NodeIndex(host)
	if i < 0 {
		return fmt.Errorf("%s is not a member", host)
	}
	current := c.Spec.Nodes[i]
	current.IP = ch.from
	cp := ch.node.Role == config.RoleControlPlane
	if cp {
		if err := m.etcdTolerates(ctx, c, current, d.Talosconfig); err != nil {
			return err
		}
	}
	if ch.kind == addrMove && addressInUse(ctx, ch.to, addressProbe) {
		return fmt.Errorf("%s already answers on the network", ch.to)
	}
	gen, err := m.machineConfigs(c, d.Bundle)
	if err != nil {
		return err
	}
	cfg := gen.Nodes[host]
	if err := m.tryConfig(ctx, ch.from, cfg, d.Talosconfig, step, host, sink); err != nil {
		return err
	}
	to, err := m.findMoved(ctx, d, ch, step, sink)
	if err != nil {
		return err
	}
	if err := confirmConfig(ctx, to, cfg, d.Talosconfig); err != nil {
		return fmt.Errorf("confirm the new config at %s: %w", to, err)
	}
	sink.Emit(Info, step, host, "answers at %s; config kept", to)
	c.Spec.Nodes[i].IP = to
	m.saveCluster(c, "")
	m.recordNode(c, c.Spec.Nodes[i])
	if cp && config.EndpointHost(d.Cluster.Spec.ControlPlane.Endpoint) == to {
		m.pinEndpoint(name, d.Cluster.Spec.ControlPlane.Endpoint)
		sink.Emit(Info, step, host, "the API endpoint is now %s", d.Cluster.Spec.ControlPlane.Endpoint)
	}
	sink.Emit(Info, step, host, "rebooting to prove the address persists")
	if err := m.rebootAndWait(ctx, to, d.Talosconfig, func() {}); err != nil {
		return fmt.Errorf("reboot at %s: %w", to, err)
	}
	if cp {
		if err := m.repairEtcdPeer(ctx, d, host, to, step, sink); err != nil {
			return err
		}
	}
	if err := m.waitNodeAt(ctx, name, host, to); err != nil {
		return err
	}
	sink.Emit(Info, step, host, "Ready at %s", to)
	return m.recordAddress(d, ch.node, to, step, sink)
}

func (m *Manager) tryConfig(ctx context.Context, from string, cfg, talosconfig []byte, step, host string, sink Sink) error {
	tc, err := talos.Dial(ctx, from, talosconfig)
	if err != nil {
		return err
	}
	defer tc.Close()
	details, err := applyDryRun(ctx, tc, cfg)
	if err != nil {
		return fmt.Errorf("dry run: %w", err)
	}
	if wantReboot(details) {
		return errors.New("the new config needs a reboot as well; apply its other changes first, then the address")
	}
	sink.Emit(Info, step, host, "applying the new address in try mode; Talos rolls back within %s unless it is confirmed", tryRollback)
	call, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	if err := tc.ApplyTry(call, cfg, tryRollback); err != nil && !maybeApplied(err) {
		return fmt.Errorf("apply: %w", err)
	}
	return nil
}

func maybeApplied(err error) bool {
	return errors.Is(err, io.EOF) || status.Code(err) == codes.Unavailable || status.Code(err) == codes.DeadlineExceeded
}

func (m *Manager) findMoved(ctx context.Context, d *Desired, ch addrChange, step string, sink Sink) (string, error) {
	host := ch.node.Hostname
	var found string
	err := talos.Retry(ctx, findTimeout, 3*time.Second, func() error {
		if ch.kind == addrRelease {
			lan, ok := config.Slash24(ch.from)
			if !ok {
				return fmt.Errorf("no /24 for %s", ch.from)
			}
			call, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			ip, err := talos.FindMember(call, lan, d.Talosconfig, host, ch.from)
			if err != nil {
				return talos.NotReady(err.Error())
			}
			found = ip
			return nil
		}
		if hostnameAt(ctx, ch.to, d.Talosconfig) == host {
			found = ch.to
			return nil
		}
		return talos.NotReady(fmt.Sprintf("%s not at %s yet", host, ch.to))
	})
	if err == nil {
		return found, nil
	}
	want := ch.to
	if want == "" {
		want = "a DHCP address"
	}
	sink.Emit(Warn, step, host, "not found at %s; waiting for Talos to roll back", want)
	back := talos.Retry(ctx, tryRollback+time.Minute, 5*time.Second, func() error {
		if hostnameAt(ctx, ch.from, d.Talosconfig) == host {
			return nil
		}
		return talos.NotReady("not back yet")
	})
	if back == nil {
		return "", fmt.Errorf("did not answer at %s within %s; Talos restored %s. Check the prefix, gateway and VLAN", want, findTimeout, ch.from)
	}
	return "", fmt.Errorf("answers at neither %s nor %s; Talos restores the previous config within %s of the apply", ch.from, want, tryRollback)
}

func hostnameAt(ctx context.Context, ip string, talosconfig []byte) string {
	call, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tc, err := talos.Dial(call, ip, talosconfig)
	if err != nil {
		return ""
	}
	defer tc.Close()
	h, _ := tc.Hostname(call)
	return h
}

func confirmConfig(ctx context.Context, ip string, cfg, talosconfig []byte) error {
	tc, err := talos.Dial(ctx, ip, talosconfig)
	if err != nil {
		return err
	}
	defer tc.Close()
	call, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	return tc.ApplyNoReboot(call, cfg)
}

func (m *Manager) repairEtcdPeer(ctx context.Context, d *Desired, host, addr, step string, sink Sink) error {
	call, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	tc, err := talos.Dial(call, addr, d.Talosconfig)
	if err != nil {
		return err
	}
	defer tc.Close()
	var peers []talos.EtcdPeer
	if err := talos.Retry(ctx, bootstrapTimeout, 5*time.Second, func() error {
		var err error
		peers, err = tc.EtcdPeers(call)
		return err
	}); err != nil {
		return fmt.Errorf("etcd members: %w", err)
	}
	want := peerURL(addr)
	for _, peer := range peers {
		if peer.Hostname != host {
			continue
		}
		if slices.Contains(peer.PeerURLs, want) {
			return m.waitEtcd(ctx, tc, d)
		}
		cfg, err := etcdClientTLS(d.Bundle)
		if err != nil {
			return err
		}
		var errs []error
		for _, ip := range m.etcdClientIPs(d, addr) {
			if err := etcdMemberUpdate(ctx, cfg, ip, peer.ID, []string{want}); err != nil {
				errs = append(errs, err)
				continue
			}
			sink.Emit(Info, step, host, "etcd peer URL %s", want)
			return m.waitEtcd(ctx, tc, d)
		}
		return errors.Join(errs...)
	}
	return fmt.Errorf("%s is not an etcd member", host)
}

func (m *Manager) etcdClientIPs(d *Desired, first string) []string {
	out := []string{first}
	if c, _, err := m.LoadCluster(d.Cluster.Metadata.Name); err == nil {
		for _, n := range c.ControlPlanes() {
			if !slices.Contains(out, n.IP) {
				out = append(out, n.IP)
			}
		}
	}
	return out
}

func (m *Manager) waitEtcd(ctx context.Context, tc *talos.Client, d *Desired) error {
	if err := m.waitEtcdMembers(ctx, tc, len(d.Cluster.ControlPlanes())); err != nil {
		return fmt.Errorf("etcd: %w", err)
	}
	return nil
}

func (m *Manager) waitNodeAt(ctx context.Context, name, host, ip string) error {
	kc, err := m.KubeClient(ctx, name)
	if err != nil {
		return err
	}
	return kc.WaitNodes(ctx, []string{host}, readyTimeout, func(n k8s.NodeStatus) bool { return n.Ready && n.InternalIP == ip }, nil)
}

func (m *Manager) recordAddress(d *Desired, n config.Node, ip, step string, sink Sink) error {
	if n.IP == ip {
		return nil
	}
	if err := repo.SetNodeIP(d.Dir, n.Hostname, ip); err != nil {
		return fmt.Errorf("record %s in cluster.yaml: %w", ip, err)
	}
	sink.Emit(Info, step, n.Hostname, "cluster.yaml: ip %s; commit it", ip)
	return nil
}
