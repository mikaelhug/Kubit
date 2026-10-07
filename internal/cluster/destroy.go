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
	"github.com/mikaelhug/kubit/internal/talos"
)

const maintenanceTimeout = 10 * time.Minute

func CheckDestroy(c *config.Cluster, confirm string) error {
	if confirm != c.Metadata.Name {
		return fmt.Errorf("type the cluster name %q to destroy it", c.Metadata.Name)
	}
	return nil
}

func (m *Manager) Destroy(ctx context.Context, d *Desired, confirm, holder string, sink Sink) error {
	c := d.Cluster
	if err := CheckDestroy(c, confirm); err != nil {
		return err
	}
	var unlock func()
	if err := sink.Run("lock", func() (err error) {
		unlock, err = m.LockApply(ctx, d, holder)
		return err
	}); err != nil {
		return err
	}
	defer unlock()
	var ls *liveState
	_ = sink.Run("observe", func() error {
		ls = m.observe(ctx, d)
		return nil
	})
	var missed []string
	for _, role := range []config.Role{config.RoleWorker, config.RoleControlPlane} {
		var nodes []config.Node
		for _, n := range c.Spec.Nodes {
			ln := ls.nodes[n.Hostname]
			switch {
			case n.Role != role:
			case ln.maint:
				sink.Emit(Info, "observe", n.Hostname, "already in maintenance mode at %s", ln.addr)
			case !ln.member:
				sink.Emit(Warn, "observe", n.Hostname, "not reachable as a member; not reset")
				missed = append(missed, n.Hostname)
			default:
				nodes = append(nodes, n)
			}
		}
		if len(nodes) == 0 {
			continue
		}
		step := map[config.Role]string{config.RoleWorker: "reset workers", config.RoleControlPlane: "reset control planes"}[role]
		if err := sink.Run(step, func() error { return m.wipeNodes(ctx, d, ls, nodes, sink) }); err != nil {
			return err
		}
	}
	if err := sink.Run("maintenance", func() error { return waitMaintenance(ctx, c.Spec.Nodes, missed, sink) }); err != nil {
		return err
	}
	if len(missed) > 0 {
		return fmt.Errorf("not reset: %s", strings.Join(missed, ", "))
	}
	machines := "machines"
	if len(c.Spec.Nodes) == 1 {
		machines = "machine"
	}
	sink.Emit(Done, "maintenance", "", "cluster %s destroyed; %d %s in maintenance mode", c.Metadata.Name, len(c.Spec.Nodes), machines)
	return nil
}

func (m *Manager) wipeNodes(ctx context.Context, d *Desired, ls *liveState, nodes []config.Node, sink Sink) error {
	var mu sync.Mutex
	var errs []error
	var wg sync.WaitGroup
	for _, n := range nodes {
		wg.Go(func() {
			addr := ls.nodes[n.Hostname].addr
			err := wipeNode(ctx, addr, d.Talosconfig)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", n.Hostname, err))
				return
			}
			sink.Emit(Info, "reset", n.Hostname, "STATE and EPHEMERAL wiped at %s; rebooting", addr)
		})
	}
	wg.Wait()
	return errors.Join(errs...)
}

func wipeNode(ctx context.Context, addr string, talosconfig []byte) error {
	tc, err := talos.Dial(ctx, addr, talosconfig)
	if err != nil {
		return err
	}
	defer tc.Close()
	call, cancel := context.WithTimeout(ctx, resetTimeout)
	defer cancel()
	return tc.WipeToMaintenance(call)
}

func waitMaintenance(ctx context.Context, nodes []config.Node, skip []string, sink Sink) error {
	pending := map[string]config.Node{}
	for _, n := range nodes {
		if !slices.Contains(skip, n.Hostname) {
			pending[n.Hostname] = n
		}
	}
	deadline := time.Now().Add(maintenanceTimeout)
	for len(pending) > 0 {
		lans := &lanScans{byPrefix: map[netip.Prefix]*lanScan{}}
		for h, n := range pending {
			if ip, ok := findMaintenance(ctx, n, lans); ok {
				sink.Emit(Done, "maintenance", h, "maintenance mode at %s", ip)
				delete(pending, h)
			}
		}
		if len(pending) == 0 {
			break
		}
		if time.Now().After(deadline) {
			var left []string
			for h := range pending {
				left = append(left, h)
			}
			return fmt.Errorf("not seen in maintenance mode after %s: %s", maintenanceTimeout, strings.Join(left, ", "))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}
	return nil
}

func findMaintenance(ctx context.Context, n config.Node, lans *lanScans) (string, bool) {
	for _, ip := range []string{n.TargetIP(), n.IP} {
		if r := talos.Probe(ctx, ip, 3*time.Second); r.Err == nil && r.State == talos.StateMaintenance && sameMachine(r, n) {
			return ip, true
		}
	}
	if n.MAC == "" {
		return "", false
	}
	lan, ok := config.Slash24(n.IP)
	if !ok {
		return "", false
	}
	for _, s := range lans.of(ctx, lan) {
		if s.Err == nil && s.State == talos.StateMaintenance && sameMachine(s, n) {
			return s.IP, true
		}
	}
	return "", false
}
