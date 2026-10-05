package cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"go.yaml.in/yaml/v4"
)

func (m *Manager) Create(ctx context.Context, c *config.Cluster, bundle *secrets.Bundle, sink Sink) error {
	name := c.Metadata.Name
	sink.Plan(createSteps...)
	if row, err := m.Store.GetCluster(ctx, name); err == nil {
		if Observable(row.State) {
			return fmt.Errorf("cluster %q already exists (%s)", name, row.State)
		}
		stored, err := config.Parse(row.Spec)
		if err != nil {
			return err
		}
		if !sameNodes(stored, c) {
			return fmt.Errorf("cluster %q is %s with a different node set; forget it first", name, row.State)
		}
		sink.Emit(Info, "preflight", "", "resuming %s cluster %s", row.State, name)
		sink.Skip("schematic")
		sink.Skip("secrets")
		return m.resume(ctx, stored, true, sink)
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if err := sink.Run("preflight", func() error { return m.preflight(ctx, c, c.Spec.Nodes, sink) }); err != nil {
		return err
	}

	if err := sink.Run("schematic", func() error {
		sink.Emit(Info, "schematic", "", "resolving Image Factory schematic for %v", c.Spec.Extensions)
		if err := m.EnsureSchematic(ctx, c); err != nil {
			return err
		}
		sink.Emit(Info, "schematic", "", "installer %s", m.installerImage(c))
		return nil
	}); err != nil {
		return err
	}

	if err := sink.Run("secrets", func() error {
		gen, err := config.Generate(c, bundle, m.installer(c))
		if err != nil {
			return err
		}
		raw, err := yaml.Marshal(gen.Secrets)
		if err != nil {
			return err
		}
		talosconfig, err := gen.Talosconfig.Bytes()
		if err != nil {
			return err
		}
		if err := m.SaveCluster(ctx, c, StateProvisioning); err != nil {
			return err
		}
		if err := m.Store.PutClusterSecrets(ctx, name, store.ClusterSecrets{SecretsBundle: raw, Talosconfig: talosconfig}); err != nil {
			return err
		}
		for _, n := range c.Spec.Nodes {
			if err := m.recordNode(ctx, c, n, NodeDiscovered, gen.Nodes[n.Hostname]); err != nil {
				return err
			}
		}
		_ = m.Store.Audit(ctx, name, "cluster.create", marshalJSON(c.Spec.Nodes))
		sink.Emit(Info, "secrets", "", "cluster secrets and %d machine configs stored", len(gen.Nodes))
		return nil
	}); err != nil {
		return err
	}
	return m.resume(ctx, c, false, sink)
}

var createSteps = Steps(
	"preflight", "Check nodes are in maintenance mode",
	"schematic", "Resolve Image Factory schematic",
	"secrets", "Generate cluster secrets and machine configs",
	"install", "Apply configs and install to disk",
	"bootstrap", "Bootstrap etcd",
	"kubeconfig", "Fetch admin kubeconfig",
	"ready", "Wait for nodes to become Ready",
)

func (m *Manager) resume(ctx context.Context, c *config.Cluster, recheck bool, sink Sink) error {
	name := c.Metadata.Name
	sec, err := m.Store.GetClusterSecrets(ctx, name)
	if err != nil {
		return err
	}
	_ = m.Store.SetClusterState(ctx, name, StateProvisioning)
	fail := func(err error) error {
		_ = m.Store.SetClusterState(ctx, name, StateFailed)
		return err
	}

	err = sink.Run("install", func() error {
		pending, cfgs, err := m.pendingInstall(ctx, c, sec.Talosconfig, sink)
		if err != nil {
			return err
		}
		if recheck && len(pending) > 0 {
			if err := m.preflight(ctx, c, pending, sink); err != nil {
				return err
			}
		}
		return m.installAll(ctx, c, pending, cfgs, sec.Talosconfig, sink)
	})
	if err != nil {
		return fail(err)
	}

	cp1 := c.ControlPlanes()[0]
	if err := sink.Run("bootstrap", func() error {
		return m.bootstrap(ctx, cp1, sec.Talosconfig, len(c.ControlPlanes()), sink)
	}); err != nil {
		return fail(err)
	}

	err = sink.Run("kubeconfig", func() error {
		if sec.Kubeconfig == nil {
			kubeconfig, err := m.fetchKubeconfig(ctx, cp1, sec.Talosconfig, sink)
			if err != nil {
				return err
			}
			if err := m.Store.SetKubeconfig(ctx, name, kubeconfig); err != nil {
				return err
			}
		} else {
			sink.Emit(Info, "kubeconfig", "", "kubeconfig already stored")
		}
		return m.Store.SetClusterState(ctx, name, StateBootstrapped)
	})
	if err != nil {
		return fail(err)
	}

	if err := sink.Run("ready", func() error { return m.waitReady(ctx, c, c.Spec.Nodes, sink) }); err != nil {
		sink.Emit(Warn, "ready", "", "%v — the cluster is up (etcd + API) but not all nodes are Ready yet; it stays usable and Create can be re-run", err)
		return err
	}
	sink.Emit(Done, "ready", "", "cluster %s is up: %d control planes, %d workers", name, len(c.ControlPlanes()), len(c.Workers()))
	return nil
}

func (m *Manager) pendingInstall(ctx context.Context, c *config.Cluster, talosconfig []byte, sink Sink) ([]config.Node, map[string][]byte, error) {
	var pending []config.Node
	cfgs := map[string][]byte{}
	moved := false
	for i, n := range c.Spec.Nodes {
		if installed, ipMoved := m.installedAt(ctx, c, i, talosconfig, sink); installed {
			moved = moved || ipMoved
			continue
		}
		cfg, err := m.Store.GetNodeMachineConfig(ctx, n.IP)
		if err != nil {
			return nil, nil, err
		}
		cfgs[n.Hostname] = cfg
		pending = append(pending, n)
	}
	if moved {
		if err := m.saveExisting(ctx, c); err != nil {
			return nil, nil, fmt.Errorf("record the new addresses: %w", err)
		}
	}
	return pending, cfgs, nil
}

func (m *Manager) installedAt(ctx context.Context, c *config.Cluster, i int, talosconfig []byte, sink Sink) (installed, moved bool) {
	n := c.Spec.Nodes[i]
	probe, cancel := context.WithTimeout(ctx, 10*time.Second)
	stage, err := talos.Stage(probe, n.TargetIP(), talosconfig)
	cancel()
	if err != nil || stage == "maintenance" {
		return false, false
	}
	sink.Emit(Info, "install", n.Hostname, "already installed (stage %s)", stage)
	if target := n.TargetIP(); target != n.IP {
		c.Spec.Nodes[i].IP = target
		n.IP = target
		_ = m.Store.UpsertNode(ctx, storeRow(c, n))
		moved = true
	}
	_ = m.Store.SetNodeState(ctx, n.IP, NodeJoined)
	return true, moved
}

func sameNodes(a, b *config.Cluster) bool {
	if len(a.Spec.Nodes) != len(b.Spec.Nodes) {
		return false
	}
	id := func(n config.Node) string {
		if n.MAC != "" {
			return "mac:" + strings.ToLower(n.MAC) + "/" + string(n.Role)
		}
		return "host:" + n.Hostname + "/" + string(n.Role)
	}
	seen := map[string]bool{}
	for _, n := range a.Spec.Nodes {
		seen[id(n)] = true
	}
	for _, n := range b.Spec.Nodes {
		if !seen[id(n)] {
			return false
		}
	}
	return true
}

func (m *Manager) preflight(ctx context.Context, c *config.Cluster, nodes []config.Node, sink Sink) error {
	for _, n := range nodes {
		r := talos.Probe(ctx, n.IP, 3*time.Second)
		switch {
		case r.Err != nil:
			return fmt.Errorf("%s (%s): %w", n.Hostname, n.IP, r.Err)
		case r.State != talos.StateMaintenance:
			return fmt.Errorf("%s (%s) is %s, not in maintenance mode", n.Hostname, n.IP, r.State)
		case r.Inventory.Arch != string(n.Arch):
			return fmt.Errorf("%s (%s) is %s, declared %s", n.Hostname, n.IP, r.Inventory.Arch, n.Arch)
		case n.Role == config.RoleControlPlane && r.Inventory.MemoryBytes > 0 && r.Inventory.MemoryBytes < config.MinControlPlaneBytes:
			return fmt.Errorf("%s (%s) is a control plane with only %s RAM; a control plane needs at least 2 GiB (etcd + API server) — give it more or make it a worker", n.Hostname, n.IP, HumanBytes(r.Inventory.MemoryBytes))
		case n.Role == config.RoleWorker && c.Spec.Platform.AddOns() && r.Inventory.MemoryBytes > 0 && r.Inventory.MemoryBytes < config.MinWorkerBytes:
			return fmt.Errorf("%s (%s) is a worker with only %s RAM; Talos and the kubelet leave it too little for the platform add-ons — give it at least 2 GiB", n.Hostname, n.IP, HumanBytes(r.Inventory.MemoryBytes))
		}
		sink.Emit(Info, "preflight", n.Hostname, "%s in maintenance mode, Talos %s %s, %d CPU, %s RAM", n.IP, r.Inventory.TalosVersion, r.Inventory.Arch, r.Inventory.CPUs, HumanBytes(r.Inventory.MemoryBytes))
	}
	return nil
}

func (m *Manager) installAll(ctx context.Context, c *config.Cluster, nodes []config.Node, cfgs map[string][]byte, talosconfig []byte, sink Sink) error {
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		errs  []error
		moved bool
	)
	for _, n := range nodes {
		wg.Add(1)
		go func(n config.Node) {
			defer wg.Done()
			err := m.installOne(ctx, n, cfgs[n.Hostname], talosconfig, sink)
			state := NodeJoined
			if err != nil {
				state = NodeFailed
				mu.Lock()
				errs = append(errs, fmt.Errorf("%s: %w", n.Hostname, err))
				mu.Unlock()
			} else if target := n.TargetIP(); target != n.IP {
				mu.Lock()
				if i := c.NodeIndex(n.Hostname); i >= 0 {
					c.Spec.Nodes[i].IP = target
				}
				moved = true
				mu.Unlock()
				n.IP = target
				_ = m.Store.UpsertNode(ctx, storeRow(c, n))
			}
			_ = m.Store.SetNodeState(ctx, n.IP, state)
		}(n)
	}
	wg.Wait()
	if moved {
		if err := m.saveExisting(ctx, c); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) installOne(ctx context.Context, n config.Node, cfg []byte, talosconfig []byte, sink Sink) error {
	_ = m.Store.SetNodeState(ctx, n.IP, NodeInstalling)
	tc, err := talos.DialMaintenance(ctx, n.IP)
	if err != nil {
		return err
	}
	bootID, err := readBootID(ctx, tc)
	if err != nil {
		tc.Close()
		return fmt.Errorf("boot id: %w", err)
	}
	err = applyConfig(ctx, tc, cfg, applyTimeout)
	tc.Close()
	if err != nil {
		return fmt.Errorf("apply: %w", err)
	}
	target := n.TargetIP()
	if target != n.IP {
		sink.Emit(Info, "install", n.Hostname, "config applied, installing to disk and rebooting; expecting it on static %s", target)
	} else {
		sink.Emit(Info, "install", n.Hostname, "config applied, installing to disk and rebooting")
	}
	if err := talos.WaitForReboot(ctx, target, talosconfig, bootID, m.Timeouts.Install); err != nil {
		return err
	}
	sink.Emit(Info, "install", n.Hostname, "rebooted into the installed system at %s", target)
	return nil
}

func (m *Manager) bootstrap(ctx context.Context, cp config.Node, talosconfig []byte, wantMembers int, sink Sink) error {
	tc, err := talos.Dial(ctx, cp.IP, talosconfig)
	if err != nil {
		return err
	}
	defer tc.Close()
	probe, cancel := context.WithTimeout(ctx, 15*time.Second)
	healthy, _ := tc.ServiceHealthy(probe, "etcd")
	cancel()
	if healthy {
		sink.Emit(Info, "bootstrap", cp.Hostname, "etcd already bootstrapped")
	} else {
		sink.Emit(Info, "bootstrap", cp.Hostname, "bootstrapping etcd")
		err = talos.Retry(ctx, m.Timeouts.Bootstrap, 5*time.Second, func() error {
			call, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			return tc.BootstrapEtcd(call)
		})
		if err != nil {
			return fmt.Errorf("bootstrap: %w", err)
		}
	}
	sink.Emit(Info, "bootstrap", cp.Hostname, "waiting for etcd (%d members expected)", wantMembers)
	if err := m.waitEtcdMembers(ctx, tc, wantMembers); err != nil {
		return fmt.Errorf("etcd: %w", err)
	}
	sink.Emit(Info, "bootstrap", cp.Hostname, "etcd healthy with %d members", wantMembers)
	return nil
}

func (m *Manager) waitEtcdMembers(ctx context.Context, tc *talos.Client, want int) error {
	return talos.Retry(ctx, m.Timeouts.Bootstrap, 5*time.Second, func() error {
		call, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		ok, err := tc.ServiceHealthy(call, "etcd")
		if err != nil {
			return err
		}
		if !ok {
			return talos.NotReady("etcd not healthy yet")
		}
		n, err := tc.EtcdMemberCount(call)
		if err != nil {
			return err
		}
		if n < want {
			return talos.NotReady(fmt.Sprintf("etcd has %d/%d members", n, want))
		}
		return nil
	})
}

func (m *Manager) fetchKubeconfig(ctx context.Context, cp config.Node, talosconfig []byte, sink Sink) ([]byte, error) {
	tc, err := talos.Dial(ctx, cp.IP, talosconfig)
	if err != nil {
		return nil, err
	}
	defer tc.Close()
	var kc []byte
	err = talos.Retry(ctx, m.Timeouts.Bootstrap, 5*time.Second, func() error {
		var err error
		kc, err = adminKubeconfig(ctx, tc, 30*time.Second)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("kubeconfig: %w", err)
	}
	sink.Emit(Info, "kubeconfig", cp.Hostname, "admin kubeconfig stored")
	return kc, nil
}

func (m *Manager) waitReady(ctx context.Context, c *config.Cluster, nodes []config.Node, sink Sink) error {
	kc, err := m.KubeClient(ctx, c.Metadata.Name)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(nodes))
	for _, n := range nodes {
		names = append(names, n.Hostname)
	}
	sink.Emit(Info, "ready", "", "waiting for %d nodes to register and become Ready", len(names))
	err = kc.WaitReady(ctx, names, m.Timeouts.Ready, func(ready, total int) {
		sink.Emit(Info, "ready", "", "%d/%d nodes Ready", ready, total)
	})
	if err != nil {
		return err
	}
	for _, n := range nodes {
		_ = m.Store.SetNodeState(ctx, n.IP, NodeReady)
	}
	if err := m.Store.SetClusterState(ctx, c.Metadata.Name, StateReady); err != nil {
		return err
	}
	return nil
}
