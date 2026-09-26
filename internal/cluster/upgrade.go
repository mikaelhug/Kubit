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

// nodeStep is the step id for per-node phases of rolling operations.
func nodeStep(n config.Node) string { return "node:" + n.Hostname }

func nodeSteps(nodes []config.Node, verb string) []Step {
	out := make([]Step, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, Step{ID: nodeStep(n), Title: verb + " " + n.Hostname, Node: n.Hostname, Status: StepPending})
	}
	return out
}

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
	schematic, pools, err := m.desiredSchematics(ctx, c)
	if err != nil {
		return err
	}
	reimage := imageOutdated(c, schematic, pools)
	if version == c.Spec.TalosVersion && !reimage {
		sink.emit(Info, "upgrade", "", "cluster already on Talos %s with its current extensions", version)
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
	imageFor := func(n config.Node) string {
		if id, ok := pools[c.PoolOf(n).Name]; ok {
			return m.Factory.InstallerImage(id, version)
		}
		return m.Factory.InstallerImage(schematic, version)
	}
	var gen *config.Generated
	if reimage {
		_, bundle, err := m.loadSecrets(ctx, name)
		if err != nil {
			return err
		}
		next := *c
		next.Spec.SchematicID = schematic
		next.Spec.Pools = append([]config.Pool(nil), c.Spec.Pools...)
		for i := range next.Spec.Pools {
			if id, ok := pools[next.Spec.Pools[i].Name]; ok {
				next.Spec.Pools[i].SchematicID = id
			}
		}
		if gen, err = config.Generate(&next, bundle, func(p config.Pool) string { return m.Factory.InstallerImage(next.SchematicFor(p), version) }); err != nil {
			return err
		}
	}
	nodes := orderedNodes(c)
	sink.plan(append(upgradePrechecks, nodeSteps(nodes, "Upgrade")...)...)
	if reimage {
		sink.emit(Info, "precheck", "", "Talos %s → %s with a new image: extensions %s", c.Spec.TalosVersion, version, strings.Join(c.Spec.Extensions, ", "))
	} else {
		sink.emit(Info, "precheck", "", "Talos %s → %s", c.Spec.TalosVersion, version)
	}
	if err := sink.run("precheck", func() error { return m.precheckUpgrade(ctx, c, kc, sec.Talosconfig, "talos", version, sink) }); err != nil {
		return err
	}
	if err := sink.run("snapshot", func() error { return m.preUpgradeSnapshot(ctx, name, sink) }); err != nil {
		return err
	}
	_ = m.Store.Audit(ctx, name, "upgrade.talos", version)

	for _, n := range nodes {
		step := nodeStep(n)
		err := sink.run(step, func() error {
			if gen != nil {
				if err := m.applyNodeConfig(ctx, n, gen.Nodes[n.Hostname], sec.Talosconfig, step, sink); err != nil {
					return fmt.Errorf("machine config for the new extensions: %w", err)
				}
			}
			dial, cancel := context.WithTimeout(ctx, 30*time.Second)
			tc, err := talos.Dial(dial, n.IP, sec.Talosconfig)
			cancel()
			if err != nil {
				return err
			}
			v, err := tc.Version(tc.Context(ctx))
			if err == nil && !reimage && len(v.Messages) > 0 && v.Messages[0].Version.Tag == version {
				tc.Close()
				sink.emit(Info, step, n.Hostname, "already on %s", version)
				return nil
			}
			bootID, err := tc.BootID(ctx)
			if err != nil {
				tc.Close()
				return err
			}
			image := imageFor(n)
			sink.emit(Info, step, n.Hostname, "upgrading to %s from %s (A/B slot install, then reboot)", version, image)
			_, err = tc.Upgrade(tc.Context(ctx), image, false, false)
			tc.Close()
			if err != nil {
				return fmt.Errorf("upgrade: %w", err)
			}
			if err := talos.WaitForReboot(ctx, n.IP, sec.Talosconfig, bootID, m.Timeouts.Install); err != nil {
				return err
			}
			sink.emit(Info, step, n.Hostname, "rebooted; waiting for Ready")
			if err := kc.WaitReady(ctx, []string{n.Hostname}, m.Timeouts.Ready, nil); err != nil {
				return fmt.Errorf("after upgrade: %w", err)
			}
			sink.emit(Info, step, n.Hostname, "back on %s and Ready", version)
			return nil
		})
		if err != nil {
			return fmt.Errorf("%s: %w", n.Hostname, err)
		}
	}
	c.Spec.TalosVersion = version
	c.Spec.SchematicID = schematic
	for i := range c.Spec.Pools {
		if id, ok := pools[c.Spec.Pools[i].Name]; ok {
			c.Spec.Pools[i].SchematicID = id
		}
	}
	if err := m.SaveCluster(ctx, c, row.State); err != nil {
		return err
	}
	sink.emit(Done, nodeStep(nodes[len(nodes)-1]), "", "all nodes on Talos %s", version)
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
	nodes := orderedNodes(c)
	sink.plan(append(append(upgradePrechecks, nodeSteps(nodes, "Apply")...), Step{ID: "manifests", Title: "Sync bootstrap manifests (kube-proxy, CoreDNS, CNI)"})...)
	sink.emit(Info, "precheck", "", "Kubernetes %s → %s", prev, version)
	sec, err := m.Store.GetClusterSecrets(ctx, name)
	if err != nil {
		return err
	}
	kc, err := m.KubeClient(ctx, name)
	if err != nil {
		return err
	}
	if err := sink.run("precheck", func() error { return m.precheckUpgrade(ctx, c, kc, sec.Talosconfig, "kubernetes", version, sink) }); err != nil {
		return err
	}
	if err := sink.run("snapshot", func() error { return m.preUpgradeSnapshot(ctx, name, sink) }); err != nil {
		return err
	}
	c.Spec.KubernetesVersion = version
	_ = m.Store.Audit(ctx, name, "upgrade.kubernetes", version)
	if err := m.ApplyConfigs(ctx, c, version, sink); err != nil {
		return err
	}
	if err := sink.run("manifests", func() error { return m.SyncManifests(ctx, c, sink) }); err != nil {
		return err
	}
	if err := m.SaveCluster(ctx, c, row.State); err != nil {
		return err
	}
	sink.emit(Done, "manifests", "", "all nodes on Kubernetes %s", version)
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
