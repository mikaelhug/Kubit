package cluster

import (
	"context"

	"github.com/mikael/kubit/internal/export"
)

// Export writes the cluster's Talos layer to dir (see package export).
func (m *Manager) Export(ctx context.Context, name, dir string) error {
	c, row, err := m.LoadCluster(ctx, name)
	if err != nil {
		return err
	}
	sec, err := m.Store.GetClusterSecrets(ctx, name)
	if err != nil {
		return err
	}
	cfgs := map[string][]byte{}
	for _, n := range c.Spec.Nodes {
		cfg, err := m.Store.GetNodeMachineConfig(ctx, n.IP)
		if err != nil {
			return err
		}
		cfgs[n.Hostname] = cfg
	}
	_ = m.Store.Audit(ctx, name, "cluster.export", dir)
	return export.Write(ctx, dir, export.Input{
		Cluster: c, ClusterYAML: row.Spec, SecretsYAML: sec.SecretsBundle,
		Talosconfig: sec.Talosconfig, Kubeconfig: sec.Kubeconfig, MachineConfigs: cfgs,
	})
}
