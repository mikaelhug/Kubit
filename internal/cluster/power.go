package cluster

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mikaelhug/kubit/internal/config"
	"github.com/mikaelhug/kubit/internal/k8s"
	"github.com/mikaelhug/kubit/internal/netx"
	"github.com/mikaelhug/kubit/internal/talos"
)

const (
	drainedShutdownTimeout = 10 * time.Minute
	forcedShutdownTimeout  = 5 * time.Minute
)

func CheckShutdown(c *config.Cluster, hosts []string, down map[string]bool) (whole bool, err error) {
	gone := map[string]bool{}
	for h, d := range down {
		gone[h] = d
	}
	for _, h := range hosts {
		if c.NodeIndex(h) < 0 {
			return false, fmt.Errorf("%s is not in cluster.yaml", h)
		}
		gone[h] = true
	}
	whole = true
	for _, n := range c.Spec.Nodes {
		whole = whole && gone[n.Hostname]
	}
	if whole {
		return true, nil
	}
	if ep, ok := c.EndpointNode(); ok && gone[ep.Hostname] && !down[ep.Hostname] {
		return false, fmt.Errorf("%s is the API endpoint and the cluster has no VIP; it shuts down only with every node", ep.Hostname)
	}
	cps := c.ControlPlanes()
	var cpsDown []string
	selectsCP := false
	for _, n := range cps {
		if gone[n.Hostname] {
			cpsDown = append(cpsDown, n.Hostname)
		}
		selectsCP = selectsCP || slices.Contains(hosts, n.Hostname)
	}
	switch {
	case !selectsCP:
		return false, nil
	case len(cpsDown) == len(cps):
		return false, errors.New("the control planes shut down only with every node")
	case len(cps) == 2:
		return false, errors.New("etcd has 2 members and loses quorum when one goes down; shut down every node instead")
	case len(cpsDown) > 1:
		return false, fmt.Errorf("etcd stays healthy with one control plane down at most; %s would be down", strings.Join(cpsDown, ", "))
	}
	return false, nil
}

func selectedNodes(c *config.Cluster, hosts []string) ([]config.Node, error) {
	var out []config.Node
	for _, h := range hosts {
		if c.NodeIndex(h) < 0 {
			return nil, fmt.Errorf("%s is not a member of %s", h, c.Metadata.Name)
		}
	}
	for _, n := range orderedNodes(c) {
		for _, h := range hosts {
			if n.Hostname == h {
				out = append(out, n)
			}
		}
	}
	return out, nil
}

func byRole(nodes []config.Node) (workers, cps []config.Node) {
	for _, n := range nodes {
		if n.Role == config.RoleControlPlane {
			cps = append(cps, n)
		} else {
			workers = append(workers, n)
		}
	}
	return workers, cps
}

func workersFirst(nodes []config.Node) []config.Node {
	workers, cps := byRole(nodes)
	return append(workers, cps...)
}

func (m *Manager) powerTargets(ctx context.Context, name string, hosts []string) (*config.Cluster, []byte, *k8s.Client, []config.Node, error) {
	c, _, err := m.LoadCluster(name)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	nodes, err := selectedNodes(c, hosts)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	sec, kc, err := m.clusterClients(ctx, name)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return c, sec.Talosconfig, kc, nodes, nil
}

func (m *Manager) lock(ctx context.Context, d *Desired, holder string, sink Sink) (func(), error) {
	var unlock func()
	err := sink.Run("lock", func() (err error) {
		unlock, err = m.LockApply(ctx, d, holder)
		return err
	})
	if err != nil {
		return nil, err
	}
	return sync.OnceFunc(unlock), nil
}

func (m *Manager) RestartNodes(ctx context.Context, d *Desired, hosts []string, holder string, sink Sink) error {
	c, talosconfig, kc, nodes, err := m.powerTargets(ctx, d.Cluster.Metadata.Name, hosts)
	if err != nil {
		return err
	}
	unlock, err := m.lock(ctx, d, holder, sink)
	if err != nil {
		return err
	}
	defer unlock()
	for _, n := range nodes {
		cy := nodeCycle{c: c, kc: kc, n: n, talosconfig: talosconfig, step: nodeStep(n), sink: sink}
		err := sink.Run(cy.step, func() error {
			if n.Role == config.RoleControlPlane {
				if err := m.etcdTolerates(ctx, c, n, talosconfig); err != nil {
					return err
				}
			}
			reboot := func() (bool, error) { return m.rebootRequested(ctx, cy, "rebooting") }
			if err := m.drainReboot(ctx, cy, cordonByRestart, "", reboot, nil); err != nil {
				return err
			}
			sink.Emit(Info, cy.step, n.Hostname, "back and Ready")
			return nil
		})
		if err != nil {
			return fmt.Errorf("%s: %w", n.Hostname, err)
		}
	}
	sink.Emit(Done, "restart", "", "%s restarted", countNodes(len(nodes)))
	return nil
}

func (m *Manager) ShutdownNodes(ctx context.Context, d *Desired, hosts []string, down map[string]bool, whole bool, holder string, sink Sink) error {
	name := d.Cluster.Metadata.Name
	c, talosconfig, kc, nodes, err := m.powerTargets(ctx, name, hosts)
	if err != nil {
		return err
	}
	unlock, err := m.lock(ctx, d, holder, sink)
	if err != nil {
		return err
	}
	defer unlock()
	gone := m.OffNodes(name)
	for h, off := range down {
		gone[h] = gone[h] || off
	}
	now, err := CheckShutdown(d.Cluster, hosts, gone)
	if err != nil {
		return err
	}
	if now != whole {
		return errors.New("the cluster changed since the selection; select again")
	}
	cycle := func(n config.Node) nodeCycle {
		return nodeCycle{c: c, kc: kc, n: n, talosconfig: talosconfig, step: nodeStep(n), sink: sink}
	}
	if whole {
		return m.shutdownCluster(ctx, c, nodes, cycle, unlock, sink)
	}
	for _, n := range workersFirst(nodes) {
		cy := cycle(n)
		err := sink.Run(cy.step, func() error {
			if n.Role == config.RoleControlPlane {
				if err := m.etcdTolerates(ctx, c, n, talosconfig); err != nil {
					return err
				}
			}
			m.markOff(ctx, cy)
			return m.shutdownOne(ctx, cy, false)
		})
		if err != nil {
			return fmt.Errorf("%s: %w", n.Hostname, err)
		}
	}
	sink.Emit(Done, "shutdown", "", "%s off", countNodes(len(nodes)))
	return nil
}

func (m *Manager) shutdownCluster(ctx context.Context, c *config.Cluster, nodes []config.Node, cycle func(config.Node) nodeCycle, unlock func(), sink Sink) error {
	if err := sink.Run("snapshot", func() error { return m.safetySnapshot(ctx, c.Metadata.Name, "pre-shutdown", sink) }); err != nil {
		sink.Emit(Warn, "snapshot", "", "continuing without a snapshot")
	}
	workers, cps := byRole(nodes)
	for _, n := range nodes {
		m.markOff(ctx, cycle(n))
	}
	if err := m.shutdownAll(ctx, workers, cycle); err != nil {
		for _, n := range cps {
			m.unmarkOff(ctx, cycle(n).kc, c.Metadata.Name, n.Hostname)
		}
		return err
	}
	unlock()
	if err := m.shutdownAll(ctx, cps, cycle); err != nil {
		return err
	}
	sink.Emit(Done, "shutdown", "", "%s is off", c.Metadata.Name)
	return nil
}

func (m *Manager) shutdownAll(ctx context.Context, nodes []config.Node, cycle func(config.Node) nodeCycle) error {
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		failed []error
	)
	for _, n := range nodes {
		wg.Go(func() {
			cy := cycle(n)
			if err := cy.sink.Run(cy.step, func() error { return m.shutdownOne(ctx, cy, true) }); err != nil {
				mu.Lock()
				failed = append(failed, fmt.Errorf("%s: %w", n.Hostname, err))
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	return errors.Join(failed...)
}

func (m *Manager) shutdownOne(ctx context.Context, cy nodeCycle, force bool) error {
	n := cy.n
	timeout := drainedShutdownTimeout
	if force {
		timeout = forcedShutdownTimeout
	}
	tc, err := talos.Dial(ctx, n.IP, cy.talosconfig)
	if err == nil {
		reportWakeOnLAN(ctx, tc, cy)
		err = shutdownNode(ctx, tc, force)
		tc.Close()
	}
	if err == nil {
		if force {
			cy.sink.Emit(Info, cy.step, n.Hostname, "shutting down")
		} else {
			cy.sink.Emit(Info, cy.step, n.Hostname, "Talos drains it, then shuts down")
		}
		err = talos.WaitDown(ctx, n.IP, timeout)
	}
	if err != nil {
		m.unmarkOff(ctx, cy.kc, cy.c.Metadata.Name, n.Hostname)
		return err
	}
	cy.sink.Emit(Info, cy.step, n.Hostname, "off")
	return nil
}

func reportWakeOnLAN(ctx context.Context, tc *talos.Client, cy nodeCycle) {
	if cy.n.MAC == "" {
		return
	}
	call, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	link, armed, err := tc.WakeOnLAN(call, cy.n.MAC)
	switch {
	case err != nil:
		cy.sink.Emit(Warn, cy.step, cy.n.Hostname, "Wake-on-LAN state unknown: %v", err)
	case armed:
		cy.sink.Emit(Info, cy.step, cy.n.Hostname, "Wake-on-LAN armed on %s", link)
	default:
		cy.sink.Emit(Warn, cy.step, cy.n.Hostname, "Wake-on-LAN is off on %s; Power on cannot wake it", link)
	}
}

func (m *Manager) PowerOnNodes(ctx context.Context, d *Desired, hosts []string, sink Sink) error {
	c, _, err := m.LoadCluster(d.Cluster.Metadata.Name)
	if err != nil {
		return err
	}
	nodes, err := selectedNodes(c, hosts)
	if err != nil {
		return err
	}
	for _, n := range nodes {
		if n.MAC == "" {
			return fmt.Errorf("%s has no mac in cluster.yaml", n.Hostname)
		}
	}
	failed, sent := map[string]error{}, map[string]bool{}
	if err := sink.Run("wake", func() error {
		for round := range 3 {
			if round > 0 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Second):
				}
			}
			for _, n := range nodes {
				if err := netx.Wake(ctx, n.MAC, wakeTargets(n)); err != nil {
					failed[n.Hostname] = fmt.Errorf("%s: %w", n.Hostname, err)
				} else {
					sent[n.Hostname] = true
				}
			}
		}
		var errs []error
		for _, n := range nodes {
			if err := failed[n.Hostname]; err != nil && !sent[n.Hostname] {
				errs = append(errs, err)
				continue
			}
			sink.Emit(Info, "wake", n.Hostname, "magic packet sent to %s", n.MAC)
		}
		return errors.Join(errs...)
	}); err != nil {
		return err
	}
	sink.Emit(Done, "wake", "", "magic packets sent to %s", countNodes(len(nodes)))
	return nil
}

func wakeTargets(n config.Node) []string {
	out := []string{"255.255.255.255"}
	var pfx netip.Prefix
	ok := false
	if n.Network != nil && len(n.Network.Addresses) > 0 {
		p, err := netip.ParsePrefix(n.Network.Addresses[0])
		pfx, ok = p.Masked(), err == nil && p.Addr().Is4()
	}
	if !ok {
		pfx, ok = config.Slash24(n.IP)
	}
	if ok {
		out = append(out, netx.Broadcast(pfx).String())
	}
	return out
}

func countNodes(n int) string {
	if n == 1 {
		return "1 node"
	}
	return fmt.Sprintf("%d nodes", n)
}

func (m *Manager) markOff(ctx context.Context, cy nodeCycle) {
	name, host := cy.c.Metadata.Name, cy.n.Hostname
	m.offMu.Lock()
	if m.off == nil {
		m.off = map[string]map[string]bool{}
	}
	if m.off[name] == nil {
		m.off[name] = map[string]bool{}
	}
	m.off[name][host] = false
	m.offMu.Unlock()
	if boot := kubeBootID(ctx, cy.kc, host); boot != "" {
		if err := cy.kc.MarkOff(ctx, host, boot); err != nil {
			cy.sink.Emit(Warn, cy.step, host, "could not mark it off in Kubernetes: %v", err)
		}
	}
}

func (m *Manager) unmarkOff(ctx context.Context, kc *k8s.Client, name, host string) {
	m.offMu.Lock()
	delete(m.off[name], host)
	m.offMu.Unlock()
	uctx, cancel := cleanupCtx(ctx)
	defer cancel()
	_ = kc.ClearOff(uctx, host)
}

func (m *Manager) OffNodes(name string) map[string]bool {
	m.offMu.Lock()
	defer m.offMu.Unlock()
	out := map[string]bool{}
	for h, down := range m.off[name] {
		if down {
			out[h] = true
		}
	}
	return out
}

func (m *Manager) offSeen(name, host string, reachable bool) bool {
	m.offMu.Lock()
	defer m.offMu.Unlock()
	down, ok := m.off[name][host]
	switch {
	case !ok:
		return false
	case !reachable:
		m.off[name][host] = true
		return true
	case down:
		delete(m.off[name], host)
	}
	return false
}
