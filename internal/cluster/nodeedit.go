package cluster

import (
	"context"
	"fmt"
	"time"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/talos"
)

func (m *Manager) RenameNode(ctx context.Context, name, hostname, newName string, sink Sink) error {
	sink.Plan(Steps(
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
	err = sink.Run("check", func() error {
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
	kc, err := m.KubeClientFor(name, sec)
	if err != nil {
		return err
	}
	if err := sink.Run("drain", func() error { return kc.Drain(ctx, hostname, 5*time.Minute, sinkWriter{sink, "drain", hostname}) }); err != nil {
		return err
	}
	for i := range c.Spec.Nodes {
		if c.Spec.Nodes[i].Hostname == hostname {
			c.Spec.Nodes[i].Hostname = newName
			n = c.Spec.Nodes[i]
		}
	}
	err = sink.Run("apply", func() error {
		gen, err := config.Generate(c, bundle, m.installer(c))
		if err != nil {
			return err
		}
		tc, err := talos.Dial(ctx, n.IP, sec.Talosconfig)
		if err != nil {
			return err
		}
		defer tc.Close()
		if err := applyConfig(ctx, tc, gen.Nodes[newName]); err != nil {
			return err
		}
		if err := m.Store.PutNodeMachineConfig(ctx, n.IP, gen.Nodes[newName], config.HasSystemVolume(gen.Nodes[newName])); err != nil {
			return err
		}
		sink.Emit(Info, "apply", newName, "hostname applied without reboot; kubelet re-registers")
		return nil
	})
	if err != nil {
		return err
	}
	if err := sink.Run("register", func() error { return kc.WaitReady(ctx, []string{newName}, m.Timeouts.Ready, nil) }); err != nil {
		return err
	}
	return sink.Run("cleanup", func() error {
		if err := kc.DeleteNode(ctx, hostname); err != nil {
			return fmt.Errorf("delete old node object: %w", err)
		}
		if err := m.saveExisting(ctx, c); err != nil {
			return err
		}
		if err := m.Store.AssignNode(ctx, n.IP, name, newName, string(n.Role)); err != nil {
			return err
		}
		_ = m.Store.Audit(ctx, name, "node.rename", hostname+" → "+newName)
		sink.Emit(Done, "cleanup", newName, "renamed from %s", hostname)
		return nil
	})
}

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
	sink.Plan(steps...)
	for i := range c.Spec.Nodes {
		if c.Spec.Nodes[i].Hostname == hostname {
			c.Spec.Nodes[i].Pool = pool
			if c.Spec.Nodes[i].InstallDisk.Path == "" && c.Spec.Nodes[i].InstallDisk.Selector == nil && target.InstallDisk != nil {
				c.Spec.Nodes[i].InstallDisk = *target.InstallDisk
			}
			n = c.Spec.Nodes[i]
		}
	}
	if err := m.saveExisting(ctx, c); err != nil {
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
	sink.Emit(Done, "ready", hostname, "now in pool %s", pool)
	return nil
}

func (m *Manager) ReaddressNode(ctx context.Context, name, hostname string, network *config.NodeNetwork, newIP string, sink Sink) error {
	sink.Plan(Steps("apply", "Apply the new network configuration", "reach", "Wait for the node on its new address", "kubelet", "Restart the kubelet so the Node advertises the new address", "reboot", "Reboot the control plane so etcd re-advertises", "ready", "Wait for Ready")...)
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
	err = sink.Run("apply", func() error {
		var err error
		for _, addr := range []string{n.IP, target} {
			var tc *talos.Client
			tc, err = talos.Dial(ctx, addr, sec.Talosconfig)
			if err != nil {
				continue
			}
			apply, cancel := context.WithTimeout(ctx, 60*time.Second)
			err = tc.Apply(apply, gen.Nodes[hostname])
			cancel()
			tc.Close()
			if err == nil {
				if addr != n.IP {
					sink.Emit(Info, "apply", hostname, "%s did not answer; applied via %s", n.IP, addr)
				}
				return nil
			}
		}
		return err
	})
	if err != nil {
		return err
	}
	err = sink.Run("reach", func() error {
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
	kc, err := m.KubeClientFor(name, sec)
	if err != nil {
		return err
	}
	if target == n.IP {
		sink.Skip("kubelet")
		sink.Skip("reboot")
	} else if n.Role == config.RoleControlPlane {
		sink.Skip("kubelet")
		if err := sink.Run("reboot", func() error {
			return m.rebootAndWait(ctx, target, sec.Talosconfig, func() {
				sink.Emit(Info, "reboot", hostname, "rebooting so etcd, the API server and the kubelet start on %s", target)
			})
		}); err != nil {
			return err
		}
	} else if err := sink.Run("kubelet", func() error {
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
		sink.Skip("reboot")
	}
	if err := sink.Run("ready", func() error { return kc.WaitReady(ctx, []string{hostname}, m.Timeouts.Ready, nil) }); err != nil {
		return err
	}
	if err := m.saveExisting(ctx, c); err != nil {
		return err
	}
	for _, x := range c.Spec.Nodes {
		if x.Hostname == hostname {
			_ = m.Store.UpsertNode(ctx, storeRow(c, x))
			_ = m.Store.PutNodeMachineConfig(ctx, x.IP, gen.Nodes[hostname], config.HasSystemVolume(gen.Nodes[hostname]))
		}
	}
	_ = m.Store.Audit(ctx, name, "node.readdress", hostname+" → "+target)
	sink.Emit(Done, "ready", hostname, "reachable at %s", target)
	return nil
}
