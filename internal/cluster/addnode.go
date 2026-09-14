package cluster

import (
	"context"
	"fmt"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/store"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
)

// AddNode joins a maintenance-mode machine to an existing cluster using the stored
// secrets, then records it in cluster.yaml.
func (m *Manager) AddNode(ctx context.Context, name string, n config.Node, sink Sink) error {
	sink.plan(Steps(
		"preflight", "Check node is in maintenance mode",
		"secrets", "Generate machine config from cluster secrets",
		"install", "Apply config and install to disk",
		"ready", "Wait for the node to become Ready",
	)...)
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
	if err := sink.run("preflight", func() error { return m.preflight(ctx, c, []config.Node{n}, sink) }); err != nil {
		return err
	}
	var sec *store.ClusterSecrets
	var gen *config.Generated
	err = sink.run("secrets", func() error {
		var bundle *secrets.Bundle
		var err error
		if sec, bundle, err = m.loadSecrets(ctx, name); err != nil {
			return err
		}
		if gen, err = config.Generate(c, bundle, m.installerImage(c)); err != nil {
			return err
		}
		if err := m.recordNode(ctx, c, n, NodeDiscovered, gen.Nodes[n.Hostname]); err != nil {
			return err
		}
		if err := m.SaveCluster(ctx, c, row.State); err != nil {
			return err
		}
		_ = m.Store.Audit(ctx, name, "node.add", marshalJSON(n))
		sink.emit(Info, "secrets", n.Hostname, "machine config generated with cluster %s secrets", name)
		return nil
	})
	if err != nil {
		return err
	}
	if err := sink.run("install", func() error {
		return m.installAll(ctx, c, []config.Node{n}, gen.Nodes, sec.Talosconfig, sink)
	}); err != nil {
		return err
	}
	if err := sink.run("ready", func() error { return m.waitReady(ctx, c, []config.Node{n}, sink) }); err != nil {
		return err
	}
	sink.emit(Done, "ready", n.Hostname, "joined cluster %s as %s", name, n.Role)
	return nil
}
