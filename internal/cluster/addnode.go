package cluster

import (
	"context"
	"fmt"

	"github.com/mikael/kubit/internal/config"
)

// AddNode joins a maintenance-mode machine to an existing cluster using the stored
// secrets, then records it in cluster.yaml.
func (m *Manager) AddNode(ctx context.Context, name string, n config.Node, sink Sink) error {
	c, row, err := m.LoadCluster(ctx, name)
	if err != nil {
		return err
	}
	if row.State != StateReady && row.State != StateBootstrapped {
		return fmt.Errorf("cluster %s is %s; nodes can only join a bootstrapped cluster", name, row.State)
	}
	for _, existing := range c.Spec.Nodes {
		if existing.Hostname == n.Hostname || existing.IP == n.IP {
			return fmt.Errorf("node %s/%s already declared in cluster %s", n.Hostname, n.IP, name)
		}
	}
	c.Spec.Nodes = append(c.Spec.Nodes, n)
	if err := c.Validate(); err != nil {
		return err
	}
	if err := m.preflight(ctx, c, []config.Node{n}, sink); err != nil {
		return err
	}
	sec, bundle, err := m.loadSecrets(ctx, name)
	if err != nil {
		return err
	}
	gen, err := config.Generate(c, bundle, m.installerImage(c))
	if err != nil {
		return err
	}
	cfg := gen.Nodes[n.Hostname]
	if err := m.recordNode(ctx, c, n, NodeDiscovered, cfg); err != nil {
		return err
	}
	if err := m.SaveCluster(ctx, c, row.State); err != nil {
		return err
	}
	_ = m.Store.Audit(ctx, name, "node.add", marshalJSON(n))

	if err := m.installAll(ctx, c, []config.Node{n}, gen.Nodes, sec.Talosconfig, sink); err != nil {
		sink.emit(Error, "add", n.Hostname, "%v", err)
		return err
	}
	if err := m.waitReady(ctx, c, []config.Node{n}, sink); err != nil {
		sink.emit(Error, "add", n.Hostname, "%v", err)
		return err
	}
	sink.emit(Done, "add", n.Hostname, "joined cluster %s as %s", name, n.Role)
	return nil
}
