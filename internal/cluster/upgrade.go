package cluster

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/k8s"
	"github.com/mikael/kubit/internal/talos"
	utilversion "k8s.io/apimachinery/pkg/util/version"
)

func nodeStep(n config.Node) string { return "node:" + n.Hostname }

func orderedNodes(c *config.Cluster) []config.Node {
	return append(append([]config.Node{}, c.ControlPlanes()...), c.Workers()...)
}

func declared(c *config.Cluster) *config.Cluster {
	out := *c
	out.Spec.Nodes = slices.DeleteFunc(slices.Clone(c.Spec.Nodes), func(n config.Node) bool {
		return n.InstallDisk.Path == "" && n.InstallDisk.Selector == nil
	})
	return &out
}

func (m *Manager) upgradeTarget(ctx context.Context, name, version string) (*config.Cluster, string, error) {
	c, _, err := m.LoadCluster(name)
	if err != nil {
		return nil, "", err
	}
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	return c, version, nil
}

func (m *Manager) UpgradeTalos(ctx context.Context, name, version string, sink Sink) error {
	c, version, err := m.upgradeTarget(ctx, name, version)
	if err != nil {
		return err
	}
	schematic, err := m.Factory.CreateSchematic(ctx, c.Spec.Extensions)
	if err != nil {
		return err
	}
	reimage := schematic != c.Spec.SchematicID
	if version == c.Spec.TalosVersion && !reimage {
		sink.Emit(Info, "upgrade", "", "cluster already on Talos %s with its current extensions", version)
		return nil
	}
	sec, kc, err := m.clusterClients(ctx, name)
	if err != nil {
		return err
	}
	want := nodeImage{version: version, schematic: schematic}
	_, bundle, err := m.loadSecrets(name)
	if err != nil {
		return err
	}
	next := *c
	next.Spec.TalosVersion, next.Spec.SchematicID = version, schematic
	final, err := m.machineConfigs(&next, bundle)
	if err != nil {
		return err
	}
	staged := final
	if reimage && version != c.Spec.TalosVersion {
		current := next
		current.Spec.TalosVersion = c.Spec.TalosVersion
		if staged, err = config.Generate(declared(&current), bundle, m.installerImage(&next)); err != nil {
			return err
		}
	}
	configsFor := func(n config.Node) nodeConfigs {
		cfgs := nodeConfigs{final: final.Nodes[n.Hostname]}
		if reimage {
			cfgs.staged = staged.Nodes[n.Hostname]
		}
		return cfgs
	}
	kubeFrom, _ := liveKubelets(ctx, kc, c, c.Spec.KubernetesVersion)
	nodes := orderedNodes(declared(c))
	if reimage {
		sink.Emit(Info, "precheck", "", "Talos %s → %s with a new image: extensions %s", c.Spec.TalosVersion, version, strings.Join(c.Spec.Extensions, ", "))
	} else {
		sink.Emit(Info, "precheck", "", "Talos %s → %s", c.Spec.TalosVersion, version)
	}
	if err := sink.Run("precheck", func() error {
		return m.precheckUpgrade(ctx, c, kc, sec.Talosconfig, "talos", kubeFrom, version, sink)
	}); err != nil {
		return err
	}
	if err := sink.Run("snapshot", func() error { return m.preUpgradeSnapshot(ctx, name, sink) }); err != nil {
		return err
	}

	for _, n := range nodes {
		step := nodeStep(n)
		err := sink.Run(step, func() error {
			already, err := m.upgradeInPlace(ctx, c, kc, n, sec.Talosconfig, m.installerImage(&next), want, reimage, configsFor(n), step, sink)
			if err != nil {
				return err
			}
			if already {
				sink.Emit(Info, step, n.Hostname, "already on %s", version)
			} else {
				sink.Emit(Info, step, n.Hostname, "back on %s and Ready", version)
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("%s: %w", n.Hostname, err)
		}
	}
	m.saveCluster(&next, "")
	if err := m.syncManifestsStep(ctx, &next, kc, sink); err != nil {
		return fmt.Errorf("all nodes on Talos %s; %w; run kubit apply again to sync them", version, err)
	}
	sink.Emit(Done, manifestsStep, "", "all nodes on Talos %s", version)
	return nil
}

func (m *Manager) applyIfBehind(ctx context.Context, n config.Node, cfg, talosconfig []byte, step string, sink Sink) (rebooted bool, err error) {
	if cfg == nil {
		return false, nil
	}
	return m.applyNodeConfig(ctx, n, cfg, talosconfig, step, sink)
}

func (m *Manager) applyBehindAndWait(ctx context.Context, c *config.Cluster, kc *k8s.Client, n config.Node, cfg, talosconfig []byte, step string, sink Sink) error {
	before := kubeBootID(ctx, kc, n.Hostname)
	rebooted, err := m.applyIfBehind(ctx, n, cfg, talosconfig, step, sink)
	if err != nil || !rebooted {
		return err
	}
	return m.waitBack(ctx, c, kc, n, before, talosconfig)
}

func (m *Manager) UpgradeKubernetes(ctx context.Context, name, version string, sink Sink) error {
	c, version, err := m.upgradeTarget(ctx, name, version)
	if err != nil {
		return err
	}
	sec, kc, err := m.clusterClients(ctx, name)
	if err != nil {
		return err
	}
	prev, rolled := liveKubelets(ctx, kc, c, version)
	if rolled {
		m.saveKubernetesVersion(c, version)
		if err := sink.Run("manifests", func() error { return m.SyncManifests(ctx, c, sink) }); err != nil {
			return err
		}
		sink.Emit(Done, "manifests", "", "cluster already on Kubernetes %s", version)
		return nil
	}
	sink.Emit(Info, "precheck", "", "Kubernetes %s → %s", prev, version)
	if err := sink.Run("precheck", func() error {
		return m.precheckUpgrade(ctx, c, kc, sec.Talosconfig, "kubernetes", prev, version, sink)
	}); err != nil {
		return err
	}
	if err := sink.Run("snapshot", func() error { return m.preUpgradeSnapshot(ctx, name, sink) }); err != nil {
		return err
	}
	next := *c
	next.Spec.KubernetesVersion = version
	if err := m.ApplyConfigs(ctx, &next, version, sink); err != nil {
		return err
	}
	m.saveKubernetesVersion(c, version)
	if err := sink.Run("manifests", func() error { return m.SyncManifests(ctx, c, sink) }); err != nil {
		return err
	}
	sink.Emit(Done, "manifests", "", "all nodes on Kubernetes %s", version)
	return nil
}

func (m *Manager) saveKubernetesVersion(c *config.Cluster, version string) {
	if c.Spec.KubernetesVersion != version {
		c.Spec.KubernetesVersion = version
		m.saveCluster(c, "")
	}
}

const manifestsStep = "manifests"

func liveKubelets(ctx context.Context, kc *k8s.Client, c *config.Cluster, target string) (lowest string, rolled bool) {
	lowest = c.Spec.KubernetesVersion
	nodes, err := kc.Nodes(ctx)
	if err != nil {
		return lowest, false
	}
	kubelet := map[string]string{}
	for _, n := range nodes {
		kubelet[n.Name] = n.KubeletVersion
	}
	rolled = true
	var oldest *utilversion.Version
	for _, n := range c.Spec.Nodes {
		v := kubelet[n.Hostname]
		if v != target {
			rolled = false
		}
		if pv, err := utilversion.ParseGeneric(v); err == nil && (oldest == nil || pv.LessThan(oldest)) {
			oldest, lowest = pv, v
		}
	}
	return lowest, rolled
}

func (m *Manager) SyncManifests(ctx context.Context, c *config.Cluster, sink Sink) error {
	sec, kc, err := m.clusterClients(ctx, c.Metadata.Name)
	if err != nil {
		return err
	}
	kc.ResetDiscovery()
	var objects []map[string]any
	cp, tc, err := firstControlPlane(ctx, c.ControlPlanes(), sec.Talosconfig, func(_ config.Node, tc *talos.Client) error {
		var err error
		objects, err = bootstrapManifests(ctx, tc)
		return err
	})
	if err != nil {
		return fmt.Errorf("bootstrap manifests: %w", err)
	}
	tc.Close()
	if err := kc.ServerSideApply(ctx, objects); err != nil {
		return err
	}
	sink.Emit(Info, "manifests", cp.Hostname, "%d bootstrap manifest objects synced", len(objects))
	return nil
}

func waitKubeletVersion(ctx context.Context, kc *k8s.Client, hostname, version string, timeout time.Duration) error {
	err := kc.WaitNodes(ctx, []string{hostname}, timeout, func(n k8s.NodeStatus) bool { return n.Ready && n.KubeletVersion == version }, nil)
	if err != nil {
		return fmt.Errorf("kubelet did not reach %s and Ready within %s: %w", version, timeout, err)
	}
	return nil
}
