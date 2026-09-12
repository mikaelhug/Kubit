package cluster

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/k8s"
	"github.com/mikael/kubit/internal/talos"
)

// orderedNodes returns control planes first, then workers: the order every rolling
// operation uses.
func orderedNodes(c *config.Cluster) []config.Node {
	return append(append([]config.Node{}, c.ControlPlanes()...), c.Workers()...)
}

// UpgradeTalos rolls a new Talos release across the cluster one node at a time: Talos
// installs the new image into the inactive A/B slot, reboots, and rolls back on its own
// if the new system fails to boot. The next node starts only after the previous one is
// back and Ready.
func (m *Manager) UpgradeTalos(ctx context.Context, name, version string, sink Sink) error {
	c, row, err := m.LoadCluster(ctx, name)
	if err != nil {
		return err
	}
	if row.State != StateReady && row.State != StateBootstrapped {
		return fmt.Errorf("cluster %s is %s", name, row.State)
	}
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	if version == c.Spec.TalosVersion {
		sink.emit(Info, "upgrade", "", "cluster already on Talos %s", version)
		return nil
	}
	sec, err := m.Store.GetClusterSecrets(ctx, name)
	if err != nil {
		return err
	}
	kc, err := m.KubeClient(ctx, name)
	if err != nil {
		return err
	}
	image := m.Factory.InstallerImage(c.Spec.SchematicID, version)
	sink.emit(Info, "upgrade", "", "Talos %s → %s using %s", c.Spec.TalosVersion, version, image)
	_ = m.Store.Audit(ctx, name, "upgrade.talos", version)

	for _, n := range orderedNodes(c) {
		dial, cancel := context.WithTimeout(ctx, 30*time.Second)
		tc, err := talos.Dial(dial, n.IP, sec.Talosconfig)
		cancel()
		if err != nil {
			return fmt.Errorf("%s: %w", n.Hostname, err)
		}
		v, err := tc.Version(tc.Context(ctx))
		if err == nil && len(v.Messages) > 0 && v.Messages[0].Version.Tag == version {
			tc.Close()
			sink.emit(Info, "upgrade", n.Hostname, "already on %s", version)
			continue
		}
		bootID, err := tc.BootID(ctx)
		if err != nil {
			tc.Close()
			return fmt.Errorf("%s: %w", n.Hostname, err)
		}
		sink.emit(Info, "upgrade", n.Hostname, "upgrading")
		_, err = tc.Upgrade(tc.Context(ctx), image, false, false)
		tc.Close()
		if err != nil {
			return fmt.Errorf("%s: upgrade: %w", n.Hostname, err)
		}
		if err := talos.WaitForReboot(ctx, n.IP, sec.Talosconfig, bootID, m.Timeouts.Install); err != nil {
			return fmt.Errorf("%s: %w", n.Hostname, err)
		}
		if err := kc.WaitReady(ctx, []string{n.Hostname}, m.Timeouts.Ready, nil); err != nil {
			return fmt.Errorf("%s after upgrade: %w", n.Hostname, err)
		}
		sink.emit(Info, "upgrade", n.Hostname, "back on %s and Ready", version)
	}
	c.Spec.TalosVersion = version
	if err := m.SaveCluster(ctx, c, row.State); err != nil {
		return err
	}
	sink.emit(Done, "upgrade", "", "all nodes on Talos %s", version)
	return nil
}

// UpgradeKubernetes bumps the component images by re-applying each node's machine
// config generated for the new version (what `talosctl upgrade-k8s` does underneath),
// control planes first, waiting for each kubelet to report the new version and Ready.
func (m *Manager) UpgradeKubernetes(ctx context.Context, name, version string, sink Sink) error {
	c, row, err := m.LoadCluster(ctx, name)
	if err != nil {
		return err
	}
	if row.State != StateReady && row.State != StateBootstrapped {
		return fmt.Errorf("cluster %s is %s", name, row.State)
	}
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	if version == c.Spec.KubernetesVersion {
		sink.emit(Info, "upgrade", "", "cluster already on Kubernetes %s", version)
		return nil
	}
	prev := c.Spec.KubernetesVersion
	c.Spec.KubernetesVersion = version
	sink.emit(Info, "upgrade", "", "Kubernetes %s → %s", prev, version)
	_ = m.Store.Audit(ctx, name, "upgrade.kubernetes", version)
	if err := m.ApplyConfigs(ctx, c, version, sink); err != nil {
		return err
	}
	if err := m.SyncManifests(ctx, c, sink); err != nil {
		return err
	}
	if err := m.SaveCluster(ctx, c, row.State); err != nil {
		return err
	}
	sink.emit(Done, "upgrade", "", "all nodes on Kubernetes %s", version)
	return nil
}

// SyncManifests re-applies Talos' rendered bootstrap manifests (kube-proxy, CoreDNS,
// flannel, RBAC) from the first control plane so their images follow the configured
// Kubernetes version.
func (m *Manager) SyncManifests(ctx context.Context, c *config.Cluster, sink Sink) error {
	sec, err := m.Store.GetClusterSecrets(ctx, c.Metadata.Name)
	if err != nil {
		return err
	}
	cp := c.ControlPlanes()[0]
	tc, err := talos.Dial(ctx, cp.IP, sec.Talosconfig)
	if err != nil {
		return err
	}
	objects, err := tc.BootstrapManifests(ctx)
	tc.Close()
	if err != nil {
		return fmt.Errorf("bootstrap manifests: %w", err)
	}
	kc, err := m.KubeClient(ctx, c.Metadata.Name)
	if err != nil {
		return err
	}
	if err := kc.ServerSideApply(ctx, objects); err != nil {
		return err
	}
	sink.emit(Info, "manifests", cp.Hostname, "%d bootstrap manifest objects synced", len(objects))
	return nil
}

func waitKubeletVersion(ctx context.Context, kc *k8s.Client, hostname, version string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		nodes, err := kc.Nodes(ctx)
		if err == nil {
			for _, n := range nodes {
				if n.Name == hostname && n.Ready && n.KubeletVersion == version {
					return nil
				}
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("kubelet did not reach %s and Ready within %s", version, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}
