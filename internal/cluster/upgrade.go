package cluster

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/k8s"
	"github.com/mikael/kubit/internal/talos"
	utilversion "k8s.io/apimachinery/pkg/util/version"
)

func nodeStep(n config.Node) string { return "node:" + n.Hostname }

func nodeSteps(nodes []config.Node, verb string) []Step {
	out := make([]Step, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, Step{ID: nodeStep(n), Title: verb + " " + n.Hostname, Node: n.Hostname, Status: StepPending})
	}
	return out
}

func orderedNodes(c *config.Cluster) []config.Node {
	return append(append([]config.Node{}, c.ControlPlanes()...), c.Workers()...)
}

func (m *Manager) upgradeTarget(ctx context.Context, name, version string) (*config.Cluster, string, error) {
	c, row, err := m.LoadCluster(ctx, name)
	if err != nil {
		return nil, "", err
	}
	if !Observable(row.State) {
		return nil, "", fmt.Errorf("cluster %s is %s", name, row.State)
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
	schematic, pools, err := m.desiredSchematics(ctx, c)
	if err != nil {
		return err
	}
	reimage := imageOutdated(c, schematic, pools)
	if version == c.Spec.TalosVersion && !reimage {
		sink.Emit(Info, "upgrade", "", "cluster already on Talos %s with its current extensions", version)
		return nil
	}
	sec, kc, err := m.clusterClients(ctx, name)
	if err != nil {
		return err
	}
	wantFor := func(n config.Node) nodeImage {
		if id, ok := pools[c.PoolOf(n).Name]; ok {
			return nodeImage{version: version, schematic: id}
		}
		return nodeImage{version: version, schematic: schematic}
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
	kubeFrom, _ := liveKubelets(ctx, kc, c, c.Spec.KubernetesVersion)
	nodes := orderedNodes(c)
	sink.Plan(append(upgradePrechecks, nodeSteps(nodes, "Upgrade")...)...)
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
	_ = m.Store.Audit(ctx, name, "upgrade.talos", version)

	for _, n := range nodes {
		step := nodeStep(n)
		err := sink.Run(step, func() error {
			if gen != nil {
				if _, err := m.applyNodeConfig(ctx, n, gen.Nodes[n.Hostname], sec.Talosconfig, step, sink); err != nil {
					return fmt.Errorf("machine config for the new extensions: %w", err)
				}
			}
			want := wantFor(n)
			already, err := m.upgradeInPlace(ctx, c, kc, n, sec.Talosconfig, m.Factory.InstallerImage(want.schematic, version), want, c.SchematicFor(c.PoolOf(n)) != want.schematic, step, sink)
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
	c.Spec.TalosVersion = version
	c.Spec.SchematicID = schematic
	for i := range c.Spec.Pools {
		if id, ok := pools[c.Spec.Pools[i].Name]; ok {
			c.Spec.Pools[i].SchematicID = id
		}
	}
	if err := m.saveExisting(ctx, c); err != nil {
		return err
	}
	sink.Emit(Done, nodeStep(nodes[len(nodes)-1]), "", "all nodes on Talos %s", version)
	return nil
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
		sink.Plan(manifestsStep)
		if err := m.saveKubernetesVersion(ctx, c, version); err != nil {
			return err
		}
		if err := sink.Run("manifests", func() error { return m.SyncManifests(ctx, c, sink) }); err != nil {
			return err
		}
		sink.Emit(Done, "manifests", "", "cluster already on Kubernetes %s", version)
		return nil
	}
	nodes := orderedNodes(c)
	sink.Plan(append(append(upgradePrechecks, nodeSteps(nodes, "Apply")...), manifestsStep)...)
	sink.Emit(Info, "precheck", "", "Kubernetes %s → %s", prev, version)
	if err := sink.Run("precheck", func() error {
		return m.precheckUpgrade(ctx, c, kc, sec.Talosconfig, "kubernetes", prev, version, sink)
	}); err != nil {
		return err
	}
	if err := sink.Run("snapshot", func() error { return m.preUpgradeSnapshot(ctx, name, sink) }); err != nil {
		return err
	}
	_ = m.Store.Audit(ctx, name, "upgrade.kubernetes", version)
	next := *c
	next.Spec.KubernetesVersion = version
	if err := m.ApplyConfigs(ctx, &next, version, sink); err != nil {
		return err
	}
	if err := m.saveKubernetesVersion(ctx, c, version); err != nil {
		return err
	}
	if err := sink.Run("manifests", func() error { return m.SyncManifests(ctx, c, sink) }); err != nil {
		return err
	}
	sink.Emit(Done, "manifests", "", "all nodes on Kubernetes %s", version)
	return nil
}

func (m *Manager) saveKubernetesVersion(ctx context.Context, c *config.Cluster, version string) error {
	if c.Spec.KubernetesVersion == version {
		return nil
	}
	c.Spec.KubernetesVersion = version
	return m.saveExisting(ctx, c)
}

var manifestsStep = Step{ID: "manifests", Title: "Sync bootstrap manifests (kube-proxy, CoreDNS, CNI)"}

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
