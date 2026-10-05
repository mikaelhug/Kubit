package cluster

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/k8s"
	"github.com/mikael/kubit/internal/store"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
)

func (m *Manager) AddNode(ctx context.Context, name string, n config.Node, sink Sink) error {
	sink.Plan(Steps(
		"preflight", "Check node is in maintenance mode",
		"secrets", "Generate machine config from cluster secrets",
		"firewall", "Allow the new subnet on existing nodes",
		manifestsStep.ID, manifestsStep.Title,
		"install", "Apply config and install to disk",
		"ready", "Wait for the node to become Ready",
	)...)
	c, row, err := m.LoadCluster(ctx, name)
	if err != nil {
		return err
	}
	if !Observable(row.State) {
		return fmt.Errorf("cluster %s is %s; nodes can only join a bootstrapped cluster", name, row.State)
	}
	i, installed, moved := -1, false, false
	for j, existing := range c.Spec.Nodes {
		if existing.Hostname != n.Hostname && existing.IP != n.IP {
			continue
		}
		if !sameDeclaredNode(existing, n) || !m.addUnfinished(ctx, existing.IP) {
			return fmt.Errorf("node %s/%s already declared in cluster %s", n.Hostname, n.IP, name)
		}
		i = j
	}
	if i < 0 {
		c.Spec.Nodes = append(c.Spec.Nodes, n)
		if err := c.Validate(); err != nil {
			return err
		}
		i = len(c.Spec.Nodes) - 1
	} else {
		sink.Emit(Info, "preflight", n.Hostname, "resuming the unfinished add of %s", n.Hostname)
		sec, err := m.Store.GetClusterSecrets(ctx, name)
		if err != nil {
			return err
		}
		installed, moved = m.installedAt(ctx, c, i, sec.Talosconfig, sink)
		if !installed {
			c.Spec.Nodes[i] = n
			if err := c.Validate(); err != nil {
				return err
			}
		}
	}
	n = c.Spec.Nodes[i]
	if installed {
		sink.Skip("preflight")
	} else if err := sink.Run("preflight", func() error { return m.preflight(ctx, c, []config.Node{n}, sink) }); err != nil {
		return err
	}
	var sec *store.ClusterSecrets
	var gen *config.Generated
	err = sink.Run("secrets", func() error {
		var bundle *secrets.Bundle
		var err error
		if sec, bundle, err = m.loadSecrets(ctx, name); err != nil {
			return err
		}
		if err := m.EnsureSchematic(ctx, c); err != nil {
			return err
		}
		if gen, err = config.Generate(c, bundle, m.installer(c)); err != nil {
			return err
		}
		state := NodeDiscovered
		if installed {
			state = NodeJoined
		}
		if err := m.recordNode(ctx, c, n, state, gen.Nodes[n.Hostname]); err != nil {
			return err
		}
		if err := m.saveExisting(ctx, c); err != nil {
			return err
		}
		sink.Emit(Info, "secrets", n.Hostname, "machine config generated with cluster %s secrets", name)
		return nil
	})
	if err != nil {
		return err
	}
	if !widensFirewall(c, i) {
		sink.Skip("firewall")
		sink.Skip(manifestsStep.ID)
	} else {
		kc, err := m.KubeClientFor(name, sec)
		if err != nil {
			return err
		}
		if err := sink.Run("firewall", func() error {
			return m.applyOthers(ctx, c, kc, n.Hostname, gen.Nodes, sec.Talosconfig, "firewall", sink)
		}); err != nil {
			return err
		}
		if err := m.syncManifestsStep(ctx, c, kc, sink); err != nil {
			return err
		}
	}
	if installed {
		if moved {
			sink.Emit(Info, "install", n.Hostname, "now on %s", n.IP)
		}
		sink.Skip("install")
	} else if err := sink.Run("install", func() error {
		return m.installAll(ctx, c, []config.Node{n}, gen.Nodes, sec.Talosconfig, sink)
	}); err != nil {
		return err
	}
	if err := sink.Run("ready", func() error { return m.waitReady(ctx, c, []config.Node{n}, sink) }); err != nil {
		return err
	}
	sink.Emit(Done, "ready", n.Hostname, "joined cluster %s as %s", name, n.Role)
	return nil
}

func sameDeclaredNode(a, b config.Node) bool {
	return a.Hostname == b.Hostname && a.IP == b.IP && strings.EqualFold(a.MAC, b.MAC)
}

func (m *Manager) addUnfinished(ctx context.Context, ip string) bool {
	row, err := m.Store.GetNode(ctx, ip)
	return err == nil && slices.Contains([]string{NodeDiscovered, NodeInstalling, NodeFailed}, row.State)
}

func widensFirewall(c *config.Cluster, added int) bool {
	if !c.FirewallOn() {
		return false
	}
	others := &config.Cluster{Spec: config.Spec{Nodes: slices.Delete(slices.Clone(c.Spec.Nodes), added, added+1)}}
	return !slices.Equal(config.ClusterSubnets(others), config.ClusterSubnets(c))
}

func (m *Manager) applyOthers(ctx context.Context, c *config.Cluster, kc *k8s.Client, skip string, cfgs map[string][]byte, talosconfig []byte, step string, sink Sink) error {
	for _, n := range orderedNodes(c) {
		if n.Hostname == skip {
			continue
		}
		stored, err := m.Store.GetNodeMachineConfig(ctx, n.IP)
		if !configBehind(stored, err, cfgs[n.Hostname]) {
			continue
		}
		before := kubeBootID(ctx, kc, n.Hostname)
		rebooted, err := m.applyNodeConfig(ctx, n, cfgs[n.Hostname], talosconfig, step, sink)
		if err == nil && rebooted {
			err = m.waitBack(ctx, c, kc, n, before, talosconfig)
		} else if err == nil {
			err = kc.WaitReady(ctx, []string{n.Hostname}, m.Timeouts.Ready, nil)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", n.Hostname, err)
		}
	}
	return nil
}
