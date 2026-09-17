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
	"go.yaml.in/yaml/v4"
)

// Create provisions a new cluster from a declaration whose nodes are all in maintenance
// mode. Steps: schematic → secrets + configs → apply to every node in parallel → wait
// for each to come back with cluster credentials → bootstrap etcd on the first control
// plane → kubeconfig → all nodes Ready. Everything after the secrets are stored is
// resumable: calling Create again with the same declaration continues where it stopped.
func (m *Manager) Create(ctx context.Context, c *config.Cluster, sink Sink) error {
	name := c.Metadata.Name
	sink.plan(createSteps...)
	if row, err := m.Store.GetCluster(ctx, name); err == nil {
		if row.State == StateReady || row.State == StateBootstrapped {
			return fmt.Errorf("cluster %q already exists (%s)", name, row.State)
		}
		stored, err := config.Parse(row.Spec)
		if err != nil {
			return err
		}
		if !sameNodes(stored, c) {
			return fmt.Errorf("cluster %q is %s with a different node set; forget it first", name, row.State)
		}
		sink.emit(Info, "preflight", "", "resuming %s cluster %s", row.State, name)
		sink.skip("schematic")
		sink.skip("secrets")
		return m.resume(ctx, stored, true, sink)
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if err := sink.run("preflight", func() error { return m.preflight(ctx, c, c.Spec.Nodes, sink) }); err != nil {
		return err
	}

	if err := sink.run("schematic", func() error {
		sink.emit(Info, "schematic", "", "resolving Image Factory schematic for %v", c.Spec.Extensions)
		if err := m.EnsureSchematic(ctx, c); err != nil {
			return err
		}
		sink.emit(Info, "schematic", "", "installer %s", m.installerImage(c))
		return nil
	}); err != nil {
		return err
	}

	if err := sink.run("secrets", func() error {
		gen, err := config.Generate(c, nil, m.installer(c))
		if err != nil {
			return err
		}
		bundle, err := yaml.Marshal(gen.Secrets)
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
		if err := m.Store.PutClusterSecrets(ctx, name, store.ClusterSecrets{SecretsBundle: bundle, Talosconfig: talosconfig}); err != nil {
			return err
		}
		for _, n := range c.Spec.Nodes {
			if err := m.recordNode(ctx, c, n, NodeDiscovered, gen.Nodes[n.Hostname]); err != nil {
				return err
			}
		}
		_ = m.Store.Audit(ctx, name, "cluster.create", marshalJSON(c.Spec.Nodes))
		sink.emit(Info, "secrets", "", "cluster secrets and %d machine configs stored", len(gen.Nodes))
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

// resume drives a provisioning cluster to ready, skipping steps already completed.
// recheck re-runs preflight on nodes still to install (a fresh Create just did it).
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

	err = sink.run("install", func() error {
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
	if err := sink.run("bootstrap", func() error {
		return m.bootstrap(ctx, cp1, sec.Talosconfig, len(c.ControlPlanes()), sink)
	}); err != nil {
		return fail(err)
	}

	err = sink.run("kubeconfig", func() error {
		if sec.Kubeconfig == nil {
			kubeconfig, err := m.fetchKubeconfig(ctx, cp1, sec.Talosconfig, sink)
			if err != nil {
				return err
			}
			if err := m.Store.SetKubeconfig(ctx, name, kubeconfig); err != nil {
				return err
			}
		} else {
			sink.emit(Info, "kubeconfig", "", "kubeconfig already stored")
		}
		return m.Store.SetClusterState(ctx, name, StateBootstrapped)
	})
	if err != nil {
		return fail(err)
	}

	if err := sink.run("ready", func() error { return m.waitReady(ctx, c, c.Spec.Nodes, sink) }); err != nil {
		// etcd and the API are already up (StateBootstrapped). Nodes not going Ready in
		// time is a degraded cluster, not a failed one — leave it Bootstrapped (a re-run
		// or the nodes catching up on CNI/image pulls recovers it) rather than mislabel
		// a live cluster as failed.
		sink.emit(Warn, "ready", "", "%v — the cluster is up (etcd + API) but not all nodes are Ready yet; it stays usable and Create can be re-run", err)
		return err
	}
	sink.emit(Done, "ready", "", "cluster %s is up: %d control planes, %d workers", name, len(c.ControlPlanes()), len(c.Workers()))
	return nil
}

// pendingInstall splits nodes into those still needing a config applied (in maintenance
// mode) and those already running the installed system, and loads the stored configs.
func (m *Manager) pendingInstall(ctx context.Context, c *config.Cluster, talosconfig []byte, sink Sink) ([]config.Node, map[string][]byte, error) {
	var pending []config.Node
	cfgs := map[string][]byte{}
	moved := false
	for i, n := range c.Spec.Nodes {
		probe, cancel := context.WithTimeout(ctx, 10*time.Second)
		stage, err := talos.Stage(probe, n.TargetIP(), talosconfig)
		cancel()
		if err == nil && stage != "maintenance" {
			sink.emit(Info, "install", n.Hostname, "already installed (stage %s)", stage)
			if target := n.TargetIP(); target != n.IP {
				c.Spec.Nodes[i].IP = target
				n.IP = target
				_ = m.Store.UpsertNode(ctx, storeRow(c, n))
				moved = true
			}
			_ = m.Store.SetNodeState(ctx, n.IP, NodeJoined)
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
		if _, row, err := m.LoadCluster(ctx, c.Metadata.Name); err == nil {
			_ = m.SaveCluster(ctx, c, row.State)
		}
	}
	return pending, cfgs, nil
}

// sameNodes reports whether two cluster specs describe the same set of machines. It
// compares stable identity (MAC when present, else hostname) and role — never IP, which
// the install itself rewrites for static nodes and which DHCP changes across leases, so
// a retry of the same cluster is not rejected as "a different node set".
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

// minControlPlaneBytes is the RAM below which a Talos control plane cannot carry etcd
// and the API server; enforced in preflight so an undersized CP fails before install
// rather than after the 0/N-Ready timeout.
const minControlPlaneBytes = 2 << 30

// preflight checks every target answers the maintenance API before anything is written.
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
		case n.Role == config.RoleControlPlane && r.Inventory.MemoryBytes > 0 && r.Inventory.MemoryBytes < minControlPlaneBytes:
			return fmt.Errorf("%s (%s) is a control plane with only %s RAM; a control plane needs at least 2 GiB (etcd + API server) — give it more or make it a worker", n.Hostname, n.IP, humanBytes(r.Inventory.MemoryBytes))
		}
		sink.emit(Info, "preflight", n.Hostname, "%s in maintenance mode, Talos %s %s, %d CPU, %s RAM", n.IP, r.Inventory.TalosVersion, r.Inventory.Arch, r.Inventory.CPUs, humanBytes(r.Inventory.MemoryBytes))
	}
	return nil
}

// installAll applies configs in parallel and waits for each node to return over mTLS.
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
				// A static node leaves its maintenance-mode lease behind; from here on
				// the declaration and the machine row point at the pinned address.
				mu.Lock()
				for i := range c.Spec.Nodes {
					if c.Spec.Nodes[i].Hostname == n.Hostname {
						c.Spec.Nodes[i].IP = target
					}
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
		if _, row, err := m.LoadCluster(ctx, c.Metadata.Name); err == nil {
			if err := m.SaveCluster(ctx, c, row.State); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) installOne(ctx context.Context, n config.Node, cfg []byte, talosconfig []byte, sink Sink) error {
	_ = m.Store.SetNodeState(ctx, n.IP, NodeInstalling)
	dial, cancel := context.WithTimeout(ctx, 30*time.Second)
	tc, err := talos.DialMaintenance(dial, n.IP)
	cancel()
	if err != nil {
		return err
	}
	bootID, err := tc.BootID(ctx)
	if err != nil {
		tc.Close()
		return fmt.Errorf("boot id: %w", err)
	}
	// A lab VM boots Talos from RAM; its persistent libvirt domain must be switched to
	// disk boot BEFORE the apply triggers the install-reboot, or the reboot re-reads the
	// old definition and lands back in the RAM installer. A failure here cannot be
	// recovered by proceeding, so it is fatal (bare-metal nodes are a no-op).
	if n.MAC != "" {
		switched, derr := m.labDiskBoot(ctx, storeMachineRef{MAC: n.MAC})
		if derr != nil {
			tc.Close()
			return fmt.Errorf("%s: switch to disk boot: %w", n.Hostname, derr)
		}
		if switched {
			sink.emit(Info, "install", n.Hostname, "lab VM set to boot from disk")
		}
	}
	err = tc.Apply(ctx, cfg)
	tc.Close()
	if err != nil {
		return fmt.Errorf("apply: %w", err)
	}
	target := n.TargetIP()
	if target != n.IP {
		sink.emit(Info, "install", n.Hostname, "config applied, installing to disk and rebooting; expecting it on static %s", target)
	} else {
		sink.emit(Info, "install", n.Hostname, "config applied, installing to disk and rebooting")
	}
	if err := talos.WaitForReboot(ctx, target, talosconfig, bootID, m.Timeouts.Install); err != nil {
		return err
	}
	sink.emit(Info, "install", n.Hostname, "rebooted into the installed system at %s", target)
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
		sink.emit(Info, "bootstrap", cp.Hostname, "etcd already bootstrapped")
	} else {
		sink.emit(Info, "bootstrap", cp.Hostname, "bootstrapping etcd")
		err = talos.Retry(ctx, m.Timeouts.Bootstrap, 5*time.Second, func() error {
			call, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			return tc.BootstrapEtcd(call)
		})
		if err != nil {
			return fmt.Errorf("bootstrap: %w", err)
		}
	}
	sink.emit(Info, "bootstrap", cp.Hostname, "waiting for etcd (%d members expected)", wantMembers)
	err = talos.Retry(ctx, m.Timeouts.Bootstrap, 5*time.Second, func() error {
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
		if n < wantMembers {
			return talos.NotReady(fmt.Sprintf("etcd has %d/%d members", n, wantMembers))
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("etcd: %w", err)
	}
	sink.emit(Info, "bootstrap", cp.Hostname, "etcd healthy with %d members", wantMembers)
	return nil
}

func (m *Manager) fetchKubeconfig(ctx context.Context, cp config.Node, talosconfig []byte, sink Sink) ([]byte, error) {
	tc, err := talos.Dial(ctx, cp.IP, talosconfig)
	if err != nil {
		return nil, err
	}
	defer tc.Close()
	var kc []byte
	err = talos.Retry(ctx, m.Timeouts.Bootstrap, 5*time.Second, func() error {
		call, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		var err error
		kc, err = tc.Kubeconfig(tc.Context(call))
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("kubeconfig: %w", err)
	}
	sink.emit(Info, "kubeconfig", cp.Hostname, "admin kubeconfig stored")
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
	sink.emit(Info, "ready", "", "waiting for %d nodes to register and become Ready", len(names))
	err = kc.WaitReady(ctx, names, m.Timeouts.Ready, func(ready, total int) {
		sink.emit(Info, "ready", "", "%d/%d nodes Ready", ready, total)
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

func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	v := float64(b) / float64(div)
	if exp >= 2 && v < 100 {
		return fmt.Sprintf("%.1f%c", v, "KMGTPE"[exp])
	}
	return fmt.Sprintf("%.0f%c", v, "KMGTPE"[exp])
}
