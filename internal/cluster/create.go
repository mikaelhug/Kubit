package cluster

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/talos"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
)

func (m *Manager) Create(ctx context.Context, c *config.Cluster, bundle *secrets.Bundle, sink Sink) error {
	name := c.Metadata.Name
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
	d, err := m.Desired(name)
	if err != nil {
		return err
	}
	var gen *config.Generated
	if err := sink.Run("secrets", func() error {
		var err error
		if gen, err = config.Generate(c, bundle, m.installerImage(c)); err != nil {
			return err
		}
		m.saveCluster(c, "")
		for _, n := range c.Spec.Nodes {
			m.recordNode(c, n)
		}
		sink.Emit(Info, "secrets", "", "%d machine configs generated", len(gen.Nodes))
		return nil
	}); err != nil {
		return err
	}
	var pending []config.Node
	if err := sink.Run("preflight", func() error {
		var err error
		if pending, err = m.pendingInstall(ctx, c, d.Talosconfig, sink); err != nil {
			return err
		}
		return m.preflight(ctx, c, pending, sink)
	}); err != nil {
		return err
	}
	if err := sink.Run("install", func() error { return m.installAll(ctx, c, pending, gen.Nodes, d.Talosconfig, sink) }); err != nil {
		return err
	}
	if err := sink.Run("bootstrap", func() error {
		return m.bootstrap(ctx, c.ControlPlanes()[0], d.Talosconfig, len(c.ControlPlanes()), sink)
	}); err != nil {
		return err
	}
	m.Store.SetClusterState(name, StateBootstrapped)
	if err := sink.Run("ready", func() error { return m.waitReady(ctx, c, c.Spec.Nodes, sink) }); err != nil {
		sink.Emit(Warn, "ready", "", "%v — etcd and the API are up but not every node is Ready; kubit apply resumes", err)
		return err
	}
	sink.Emit(Done, "ready", "", "cluster %s is up: %d control planes, %d workers", name, len(c.ControlPlanes()), len(c.Workers()))
	return nil
}

func (m *Manager) pendingInstall(ctx context.Context, c *config.Cluster, talosconfig []byte, sink Sink) ([]config.Node, error) {
	var pending []config.Node
	moved := false
	for i, n := range c.Spec.Nodes {
		if installed, ipMoved := m.installedAt(ctx, c, i, talosconfig, sink); installed {
			moved = moved || ipMoved
			continue
		}
		pending = append(pending, n)
	}
	if moved {
		m.saveCluster(c, "")
	}
	return pending, nil
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
		m.recordNode(c, n)
		moved = true
	}
	return true, moved
}

func (m *Manager) preflight(ctx context.Context, c *config.Cluster, nodes []config.Node, sink Sink) error {
	for _, n := range nodes {
		r := talos.Probe(ctx, n.IP, 3*time.Second)
		switch {
		case r.Err != nil:
			return fmt.Errorf("%s (%s): %w", n.Hostname, n.IP, r.Err)
		case r.State != talos.StateMaintenance:
			return fmt.Errorf("%s (%s) is %s, not in maintenance mode", n.Hostname, n.IP, r.State)
		case !sameMachine(r, n):
			return fmt.Errorf("%s (%s) answers as another machine; cluster.yaml declares %s", n.Hostname, n.IP, n.MAC)
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
			if err != nil {
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
				m.recordNode(c, n)
			}
		}(n)
	}
	wg.Wait()
	if moved {
		m.saveCluster(c, "")
	}
	return errors.Join(errs...)
}

func (m *Manager) installOne(ctx context.Context, n config.Node, cfg []byte, talosconfig []byte, sink Sink) error {
	tc, err := talos.DialMaintenance(ctx, n.IP)
	if err != nil {
		return err
	}
	bootID, err := readBootID(ctx, tc)
	if err != nil {
		tc.Close()
		return fmt.Errorf("boot id: %w", err)
	}
	err = applyConfig(ctx, tc, cfg)
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
	if err := talos.WaitForReboot(ctx, target, talosconfig, bootID, installTimeout); err != nil {
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
		err = talos.Retry(ctx, bootstrapTimeout, 5*time.Second, func() error {
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
	return talos.Retry(ctx, bootstrapTimeout, 5*time.Second, func() error {
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
	err = kc.WaitReady(ctx, names, readyTimeout, func(ready, total int) {
		sink.Emit(Info, "ready", "", "%d/%d nodes Ready", ready, total)
	})
	if err != nil {
		return err
	}
	m.Store.SetClusterState(c.Metadata.Name, StateReady)
	return nil
}
