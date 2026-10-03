package cluster

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/talos"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
		if c.NodeIndex(newName) >= 0 {
			return fmt.Errorf("hostname %s already exists in cluster %s", newName, name)
		}
		trial := *c
		trial.Spec.Nodes = append([]config.Node(nil), c.Spec.Nodes...)
		trial.Spec.Nodes[trial.NodeIndex(hostname)].Hostname = newName
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
	i := c.NodeIndex(hostname)
	c.Spec.Nodes[i].Hostname = newName
	n = c.Spec.Nodes[i]
	err = sink.Run("apply", func() error {
		gen, err := config.Generate(c, bundle, m.installer(c))
		if err != nil {
			return err
		}
		tc, err := talos.Dial(ctx, n.IP, sec.Talosconfig)
		if err != nil {
			return err
		}
		err = applyConfig(ctx, tc, gen.Nodes[newName], applyTimeout)
		tc.Close()
		if err != nil {
			return err
		}
		if err := m.recordRename(ctx, c, n, hostname, gen.Nodes[newName]); err != nil {
			return fmt.Errorf("%s already runs as %s but the rename was not recorded: %w", hostname, newName, err)
		}
		sink.Emit(Info, "apply", newName, "hostname applied without reboot; kubelet re-registers")
		return nil
	})
	if err != nil {
		if uerr := kc.Uncordon(ctx, hostname); uerr != nil {
			sink.Emit(Warn, "apply", hostname, "could not uncordon: %v", uerr)
		}
		return err
	}
	if err := sink.Run("register", func() error {
		if err := kc.WaitReady(ctx, []string{newName}, m.Timeouts.Ready, nil); err != nil {
			return fmt.Errorf("%w; stale Node object %s not deleted", err, hostname)
		}
		return nil
	}); err != nil {
		return err
	}
	return sink.Run("cleanup", func() error {
		if err := kc.DeleteNode(ctx, hostname); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete stale Node object %s: %w", hostname, err)
		}
		sink.Emit(Done, "cleanup", newName, "renamed from %s", hostname)
		return nil
	})
}

func (m *Manager) recordRename(ctx context.Context, c *config.Cluster, n config.Node, from string, cfg []byte) error {
	if err := m.Store.PutNodeMachineConfig(ctx, n.IP, cfg, config.HasSystemVolume(cfg)); err != nil {
		return err
	}
	if err := m.saveExisting(ctx, c); err != nil {
		return err
	}
	if err := m.Store.AssignNode(ctx, n.IP, c.Metadata.Name, n.Hostname, string(n.Role)); err != nil {
		return err
	}
	_ = m.Store.Audit(ctx, c.Metadata.Name, "node.rename", from+" → "+n.Hostname)
	return nil
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
	sec, bundle, err := m.loadSecrets(ctx, name)
	if err != nil {
		return err
	}
	kc, err := m.KubeClientFor(name, sec)
	if err != nil {
		return err
	}
	from := c.PoolOf(n)
	i := c.NodeIndex(hostname)
	c.Spec.Nodes[i].Pool = pool
	if c.Spec.Nodes[i].InstallDisk.Path == "" && c.Spec.Nodes[i].InstallDisk.Selector == nil && target.InstallDisk != nil {
		c.Spec.Nodes[i].InstallDisk = *target.InstallDisk
	}
	n = c.Spec.Nodes[i]
	desired := c.SchematicFor(*target)
	current, _ := readNodeImage(ctx, n.IP, sec.Talosconfig)
	reimage := current.schematicDiffers(desired, c.SchematicFor(from))
	step := nodeStep(n)
	steps := Steps("apply", "Apply pool labels, taints and config", "ready", "Wait for Ready")
	if reimage {
		steps = append(Steps(step, "Install the pool's Talos image (reboot)"), steps...)
	}
	sink.Plan(steps...)
	if err := m.saveExisting(ctx, c); err != nil {
		return err
	}
	if reimage {
		image := m.Factory.InstallerImage(desired, c.Spec.TalosVersion)
		if err := sink.Run(step, func() error {
			return m.reinstall(ctx, c, kc, n, sec.Talosconfig, image, c.Spec.TalosVersion, step, sink)
		}); err != nil {
			return err
		}
	}
	gen, err := config.Generate(c, bundle, m.installer(c))
	if err != nil {
		return err
	}
	before := kubeBootID(ctx, kc, hostname)
	rebooted := false
	if err := sink.Run("apply", func() error {
		var err error
		rebooted, err = m.applyNodeConfig(ctx, n, gen.Nodes[hostname], sec.Talosconfig, "apply", sink)
		return err
	}); err != nil {
		return err
	}
	if err := sink.Run("ready", func() error {
		if rebooted {
			return m.waitBack(ctx, c, kc, n, before, sec.Talosconfig)
		}
		return kc.WaitReady(ctx, []string{hostname}, m.Timeouts.Ready, nil)
	}); err != nil {
		return err
	}
	_ = m.Store.UpsertNode(ctx, storeRow(c, n))
	_ = m.Store.Audit(ctx, name, "node.pool", hostname+" → "+pool)
	sink.Emit(Done, "ready", hostname, "now in pool %s", pool)
	return nil
}

func (m *Manager) ReaddressNode(ctx context.Context, name, hostname string, network *config.NodeNetwork, newIP string, sink Sink) error {
	sink.Plan(Steps("firewall", "Allow the new subnet on the other nodes", "apply", "Apply the new network configuration", "reach", "Wait for the node on its new address", "kubelet", "Restart the kubelet so the Node advertises the new address", "reboot", "Reboot the control plane so etcd re-advertises", "ready", "Wait for Ready", "narrow", "Remove the old subnet from the other nodes")...)
	c, n, err := m.findNode(ctx, name, hostname)
	if err != nil {
		return err
	}
	oldSubnets := config.ClusterSubnets(c)
	if ep, ok := c.EndpointNode(); ok && ep.Hostname == hostname && newIP != "" && newIP != n.IP {
		return fmt.Errorf("%s is the API endpoint (no VIP); re-addressing it would break every kubeconfig — set a VIP first", hostname)
	}
	i := c.NodeIndex(hostname)
	c.Spec.Nodes[i].Network = network
	if newIP != "" {
		c.Spec.Nodes[i].IP = newIP
	}
	if err := c.Validate(); err != nil {
		return err
	}
	sec, bundle, err := m.loadSecrets(ctx, name)
	if err != nil {
		return err
	}
	gen, err := config.Generate(c, bundle, m.installer(c))
	if err != nil {
		return err
	}
	kc, err := m.KubeClientFor(name, sec)
	if err != nil {
		return err
	}
	widen := c.FirewallOn() && !slices.Equal(oldSubnets, config.ClusterSubnets(c))
	if !widen {
		sink.Skip("firewall")
	} else if err := sink.Run("firewall", func() error {
		bridged, err := config.Generate(bridgeSubnets(c, i, n), bundle, m.installer(c))
		if err != nil {
			return err
		}
		return m.applyOthers(ctx, c, kc, hostname, bridged.Nodes, sec.Talosconfig, "firewall", sink)
	}); err != nil {
		return err
	}
	target := c.Spec.Nodes[i].IP
	err = sink.Run("apply", func() error {
		var err error
		for _, addr := range []string{n.IP, target} {
			var tc *talos.Client
			tc, err = talos.Dial(ctx, addr, sec.Talosconfig)
			if err != nil {
				continue
			}
			timeout := applyTimeout
			if addr == n.IP {
				timeout = readdressTimeout
			}
			err = applyConfig(ctx, tc, gen.Nodes[hostname], timeout)
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
		err := talos.Retry(ctx, m.Timeouts.Install, 3*time.Second, func() error {
			probe, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			_, err := talos.Stage(probe, target, sec.Talosconfig)
			return err
		})
		if err != nil {
			return err
		}
		if err := m.saveExisting(ctx, c); err != nil {
			return err
		}
		_ = m.Store.UpsertNode(ctx, storeRow(c, c.Spec.Nodes[i]))
		_ = m.Store.PutNodeMachineConfig(ctx, target, gen.Nodes[hostname], config.HasSystemVolume(gen.Nodes[hostname]))
		return nil
	})
	if err != nil {
		return err
	}
	before, rebooted := "", false
	if target == n.IP {
		sink.Skip("kubelet")
		sink.Skip("reboot")
	} else if n.Role == config.RoleControlPlane {
		sink.Skip("kubelet")
		before, rebooted = kubeBootID(ctx, kc, hostname), true
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
		err = restartService(ctx, tc, "kubelet")
		tc.Close()
		if err != nil {
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
	moved := c.Spec.Nodes[i]
	if err := sink.Run("ready", func() error {
		if rebooted {
			return m.waitBack(ctx, c, kc, moved, before, sec.Talosconfig)
		}
		return kc.WaitReady(ctx, []string{hostname}, m.Timeouts.Ready, nil)
	}); err != nil {
		return err
	}
	_ = m.Store.Audit(ctx, name, "node.readdress", hostname+" → "+target)
	if !widen {
		sink.Skip("narrow")
		sink.Emit(Done, "ready", hostname, "reachable at %s", target)
		return nil
	}
	if err := sink.Run("narrow", func() error {
		return m.applyOthers(ctx, c, kc, hostname, gen.Nodes, sec.Talosconfig, "narrow", sink)
	}); err != nil {
		return err
	}
	sink.Emit(Done, "narrow", hostname, "reachable at %s", target)
	return nil
}

func bridgeSubnets(c *config.Cluster, moved int, old config.Node) *config.Cluster {
	both := config.ClusterSubnets(&config.Cluster{Spec: config.Spec{Nodes: []config.Node{old, c.Spec.Nodes[moved]}}})
	addrs := make([]string, 0, len(both))
	for _, p := range both {
		addrs = append(addrs, p.String())
	}
	bridge := *c
	bridge.Spec.Nodes = slices.Clone(c.Spec.Nodes)
	bridge.Spec.Nodes[moved].Network = &config.NodeNetwork{Addresses: addrs}
	return &bridge
}
