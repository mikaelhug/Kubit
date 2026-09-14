package cluster

import (
	"context"
	"fmt"
	"time"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/talos"
)

// RenameNode changes a node's hostname. Kubernetes node names are immutable, so the
// kubelet re-registers under the new name and the old Node object is deleted; pods on
// it are drained first so nothing is orphaned.
func (m *Manager) RenameNode(ctx context.Context, name, hostname, newName string, sink Sink) error {
	sink.plan(Steps(
		"check", "Validate the new name",
		"drain", "Cordon and drain "+hostname,
		"apply", "Apply the new hostname",
		"register", "Wait for "+newName+" to register and become Ready",
		"cleanup", "Delete the old Node object "+hostname,
	)...)
	c, n, err := m.findNode(ctx, name, hostname)
	if err != nil {
		return err
	}
	err = sink.run("check", func() error {
		if newName == hostname {
			return fmt.Errorf("new name equals the current one")
		}
		for _, o := range c.Spec.Nodes {
			if o.Hostname == newName {
				return fmt.Errorf("hostname %s already exists in cluster %s", newName, name)
			}
		}
		trial := *c
		trial.Spec.Nodes = append([]config.Node(nil), c.Spec.Nodes...)
		for i := range trial.Spec.Nodes {
			if trial.Spec.Nodes[i].Hostname == hostname {
				trial.Spec.Nodes[i].Hostname = newName
			}
		}
		return trial.Validate()
	})
	if err != nil {
		return err
	}
	sec, bundle, err := m.loadSecrets(ctx, name)
	if err != nil {
		return err
	}
	kc, err := m.KubeClient(ctx, name)
	if err != nil {
		return err
	}
	if err := sink.run("drain", func() error { return kc.Drain(ctx, hostname, 5*time.Minute, sinkWriter{sink, "drain", hostname}) }); err != nil {
		return err
	}
	for i := range c.Spec.Nodes {
		if c.Spec.Nodes[i].Hostname == hostname {
			c.Spec.Nodes[i].Hostname = newName
			n = c.Spec.Nodes[i]
		}
	}
	err = sink.run("apply", func() error {
		gen, err := config.Generate(c, bundle, m.installer(c))
		if err != nil {
			return err
		}
		dial, cancel := context.WithTimeout(ctx, 30*time.Second)
		tc, err := talos.Dial(dial, n.IP, sec.Talosconfig)
		cancel()
		if err != nil {
			return err
		}
		defer tc.Close()
		if err := tc.Apply(ctx, gen.Nodes[newName]); err != nil {
			return err
		}
		if err := m.Store.PutNodeMachineConfig(ctx, n.IP, gen.Nodes[newName]); err != nil {
			return err
		}
		sink.emit(Info, "apply", newName, "hostname applied without reboot; kubelet re-registers")
		return nil
	})
	if err != nil {
		return err
	}
	if err := sink.run("register", func() error { return kc.WaitReady(ctx, []string{newName}, m.Timeouts.Ready, nil) }); err != nil {
		return err
	}
	return sink.run("cleanup", func() error {
		if err := kc.DeleteNode(ctx, hostname); err != nil {
			return fmt.Errorf("delete old node object: %w", err)
		}
		row, err := m.Store.GetCluster(ctx, name)
		if err != nil {
			return err
		}
		if err := m.SaveCluster(ctx, c, row.State); err != nil {
			return err
		}
		if err := m.Store.AssignNode(ctx, n.IP, name, newName, string(n.Role)); err != nil {
			return err
		}
		_ = m.Store.Audit(ctx, name, "node.rename", hostname+" → "+newName)
		sink.emit(Done, "cleanup", newName, "renamed from %s", hostname)
		return nil
	})
}

// MoveNodeToPool changes a node's pool: labels/taints follow at once; a different
// schematic means a single-node Talos upgrade to the pool's installer image first.
func (m *Manager) MoveNodeToPool(ctx context.Context, name, hostname, pool string, sink Sink) error {
	c, n, err := m.findNode(ctx, name, hostname)
	if err != nil {
		return err
	}
	var target *config.Pool
	for i := range c.Spec.Pools {
		if c.Spec.Pools[i].Name == pool {
			target = &c.Spec.Pools[i]
		}
	}
	if target == nil {
		return fmt.Errorf("pool %s is not declared in cluster %s", pool, name)
	}
	if target.Role != n.Role {
		return fmt.Errorf("pool %s has role %s; a %s cannot change role in place — remove and re-add it", pool, target.Role, n.Role)
	}
	if err := m.EnsureSchematic(ctx, c); err != nil {
		return err
	}
	from := c.PoolOf(n)
	reimage := c.SchematicFor(*target) != c.SchematicFor(from)
	steps := Steps("apply", "Apply pool labels, taints and config", "ready", "Wait for Ready")
	if reimage {
		steps = append(Steps(nodeStep(n), "Install the pool's Talos image (reboot)"), steps...)
	}
	sink.plan(steps...)
	for i := range c.Spec.Nodes {
		if c.Spec.Nodes[i].Hostname == hostname {
			c.Spec.Nodes[i].Pool = pool
			if c.Spec.Nodes[i].InstallDisk.Path == "" && c.Spec.Nodes[i].InstallDisk.Selector == nil && target.InstallDisk != nil {
				c.Spec.Nodes[i].InstallDisk = *target.InstallDisk
			}
			n = c.Spec.Nodes[i]
		}
	}
	row, err := m.Store.GetCluster(ctx, name)
	if err != nil {
		return err
	}
	if err := m.SaveCluster(ctx, c, row.State); err != nil {
		return err
	}
	if reimage {
		if err := m.UpgradeNode(ctx, name, hostname, c.Spec.TalosVersion, sink); err != nil {
			return err
		}
	}
	if err := m.ApplyConfigs(ctx, c, "", sink); err != nil {
		return err
	}
	_ = m.Store.UpsertNode(ctx, storeRow(c, n))
	_ = m.Store.Audit(ctx, name, "node.pool", hostname+" → "+pool)
	sink.emit(Done, "ready", hostname, "now in pool %s", pool)
	return nil
}

// ReaddressNode changes how a node gets its address. A nil network returns it to DHCP.
// Talos keeps serving the API on the new address; Kubit waits for it there.
func (m *Manager) ReaddressNode(ctx context.Context, name, hostname string, network *config.NodeNetwork, newIP string, sink Sink) error {
	sink.plan(Steps("apply", "Apply the new network configuration", "reach", "Wait for the node on its new address", "kubelet", "Restart the kubelet so the Node advertises the new address", "reboot", "Reboot the control plane so etcd re-advertises", "ready", "Wait for Ready")...)
	c, n, err := m.findNode(ctx, name, hostname)
	if err != nil {
		return err
	}
	for i := range c.Spec.Nodes {
		if c.Spec.Nodes[i].Hostname == hostname {
			c.Spec.Nodes[i].Network = network
			if newIP != "" {
				c.Spec.Nodes[i].IP = newIP
			}
		}
	}
	if err := c.Validate(); err != nil {
		return err
	}
	if c.Spec.ControlPlane.Endpoint == "https://"+n.IP+":6443" && newIP != "" && newIP != n.IP {
		return fmt.Errorf("%s is the API endpoint (no VIP); re-addressing it would break every kubeconfig — set a VIP first", hostname)
	}
	sec, bundle, err := m.loadSecrets(ctx, name)
	if err != nil {
		return err
	}
	gen, err := config.Generate(c, bundle, m.installer(c))
	if err != nil {
		return err
	}
	target := n.IP
	if newIP != "" {
		target = newIP
	}
	err = sink.run("apply", func() error {
		// After an interrupted attempt the machine may already answer on the new address;
		// gRPC dials lazily, so only the call itself tells.
		var err error
		for _, addr := range []string{n.IP, target} {
			dial, cancel := context.WithTimeout(ctx, 20*time.Second)
			var tc *talos.Client
			tc, err = talos.Dial(dial, addr, sec.Talosconfig)
			cancel()
			if err != nil {
				continue
			}
			apply, cancel := context.WithTimeout(ctx, 60*time.Second)
			err = tc.Apply(apply, gen.Nodes[hostname])
			cancel()
			tc.Close()
			if err == nil {
				if addr != n.IP {
					sink.emit(Info, "apply", hostname, "%s did not answer; applied via %s", n.IP, addr)
				}
				return nil
			}
		}
		return err
	})
	if err != nil {
		return err
	}
	err = sink.run("reach", func() error {
		return talos.Retry(ctx, m.Timeouts.Install, 3*time.Second, func() error {
			probe, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			_, err := talos.Stage(probe, target, sec.Talosconfig)
			return err
		})
	})
	if err != nil {
		return err
	}
	kc, err := m.KubeClient(ctx, name)
	if err != nil {
		return err
	}
	if target == n.IP {
		sink.skip("kubelet")
		sink.skip("reboot")
	} else if n.Role == config.RoleControlPlane {
		// etcd and the static pods bind the old address until the machine restarts;
		// a kubelet restart alone leaves etcd on this member unreachable.
		sink.skip("kubelet")
		if err := sink.run("reboot", func() error {
			tc, err := talos.Dial(ctx, target, sec.Talosconfig)
			if err != nil {
				return err
			}
			bootID, err := tc.BootID(ctx)
			if err != nil {
				tc.Close()
				return err
			}
			err = tc.Reboot(tc.Context(ctx))
			tc.Close()
			if err != nil {
				return err
			}
			sink.emit(Info, "reboot", hostname, "rebooting so etcd, the API server and the kubelet start on %s", target)
			return talos.WaitForReboot(ctx, target, sec.Talosconfig, bootID, m.Timeouts.Install)
		}); err != nil {
			return err
		}
	} else if err := sink.run("kubelet", func() error {
		// The kubelet pins --node-ip at start; without a restart the API server keeps
		// talking to the old address for logs, exec and metrics.
		tc, err := talos.Dial(ctx, target, sec.Talosconfig)
		if err != nil {
			return err
		}
		defer tc.Close()
		if err := tc.RestartService(ctx, "kubelet"); err != nil {
			return err
		}
		return talos.Retry(ctx, m.Timeouts.Ready, 3*time.Second, func() error {
			d, err := kc.NodeDetail(ctx, hostname)
			if err != nil {
				return err
			}
			if d.InternalIP != target {
				return talos.NotReady(fmt.Sprintf("node still advertises %s", d.InternalIP))
			}
			return nil
		})
	}); err != nil {
		return err
	} else {
		sink.skip("reboot")
	}
	if err := sink.run("ready", func() error { return kc.WaitReady(ctx, []string{hostname}, m.Timeouts.Ready, nil) }); err != nil {
		return err
	}
	row, err := m.Store.GetCluster(ctx, name)
	if err != nil {
		return err
	}
	if err := m.SaveCluster(ctx, c, row.State); err != nil {
		return err
	}
	for _, x := range c.Spec.Nodes {
		if x.Hostname == hostname {
			_ = m.Store.UpsertNode(ctx, storeRow(c, x))
			_ = m.Store.PutNodeMachineConfig(ctx, x.IP, gen.Nodes[hostname])
		}
	}
	_ = m.Store.Audit(ctx, name, "node.readdress", hostname+" → "+target)
	sink.emit(Done, "ready", hostname, "reachable at %s", target)
	return nil
}
