package cluster

import (
	"context"
	"fmt"

	"github.com/mikaelhug/kubit/internal/config"
)

func (m *Manager) AddNode(ctx context.Context, name string, n config.Node, sink Sink) error {
	c, _, err := m.LoadCluster(name)
	if err != nil {
		return err
	}
	for _, existing := range c.Spec.Nodes {
		if existing.Hostname == n.Hostname || existing.IP == n.IP {
			return fmt.Errorf("node %s/%s already in cluster %s", n.Hostname, n.IP, name)
		}
	}
	c.Spec.Nodes = append(c.Spec.Nodes, n)
	if err := declared(c).Validate(); err != nil {
		return err
	}
	if err := sink.Run("preflight", func() error { return m.preflight(ctx, c, []config.Node{n}, sink) }); err != nil {
		return err
	}
	sec, bundle, err := m.loadSecrets(name)
	if err != nil {
		return err
	}
	var gen *config.Generated
	err = sink.Run("secrets", func() error {
		if err := m.EnsureSchematic(ctx, c); err != nil {
			return err
		}
		if gen, err = m.machineConfigs(c, bundle); err != nil {
			return err
		}
		m.recordNode(c, n)
		m.saveCluster(c, "")
		sink.Emit(Info, "secrets", n.Hostname, "machine config generated with cluster %s secrets", name)
		return nil
	})
	if err != nil {
		return err
	}
	if err := sink.Run("install", func() error {
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
