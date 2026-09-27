package cluster

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/k8s"
	"github.com/mikael/kubit/internal/talos"
)

func (m *Manager) findNode(ctx context.Context, name, hostname string) (*config.Cluster, config.Node, error) {
	c, _, err := m.LoadCluster(ctx, name)
	if err != nil {
		return nil, config.Node{}, err
	}
	i := c.NodeIndex(hostname)
	if i < 0 {
		return nil, config.Node{}, fmt.Errorf("node %s is not part of cluster %s", hostname, name)
	}
	return c, c.Spec.Nodes[i], nil
}

func (m *Manager) CordonNode(ctx context.Context, name, hostname string, sink Sink) error {
	sink.Plan(Steps("cordon", "Mark "+hostname+" unschedulable")...)
	if _, _, err := m.findNode(ctx, name, hostname); err != nil {
		return err
	}
	return sink.Run("cordon", func() error {
		kc, err := m.KubeClient(ctx, name)
		if err != nil {
			return err
		}
		if err := kc.Cordon(ctx, hostname); err != nil {
			return err
		}
		_ = m.Store.Audit(ctx, name, "node.cordon", hostname)
		sink.Emit(Done, "cordon", hostname, "cordoned; new pods will not be scheduled here")
		return nil
	})
}

func (m *Manager) UncordonNode(ctx context.Context, name, hostname string, sink Sink) error {
	sink.Plan(Steps("uncordon", "Mark "+hostname+" schedulable")...)
	if _, _, err := m.findNode(ctx, name, hostname); err != nil {
		return err
	}
	return sink.Run("uncordon", func() error {
		kc, err := m.KubeClient(ctx, name)
		if err != nil {
			return err
		}
		if err := kc.Uncordon(ctx, hostname); err != nil {
			return err
		}
		_ = m.Store.Audit(ctx, name, "node.uncordon", hostname)
		sink.Emit(Done, "uncordon", hostname, "schedulable again")
		return nil
	})
}

func (m *Manager) DrainNode(ctx context.Context, name, hostname string, sink Sink) error {
	sink.Plan(Steps("drain", "Cordon and evict pods from "+hostname)...)
	if _, _, err := m.findNode(ctx, name, hostname); err != nil {
		return err
	}
	return sink.Run("drain", func() error {
		kc, err := m.KubeClient(ctx, name)
		if err != nil {
			return err
		}
		if err := kc.Drain(ctx, hostname, 5*time.Minute, sinkWriter{sink, "drain", hostname}); err != nil {
			return err
		}
		_ = m.Store.Audit(ctx, name, "node.drain", hostname)
		sink.Emit(Done, "drain", hostname, "drained; uncordon to schedule pods here again")
		return nil
	})
}

func (m *Manager) RebootNode(ctx context.Context, name, hostname string, drainFirst bool, sink Sink) error {
	steps := Steps("reboot", "Reboot "+hostname+" and wait for it to return", "ready", "Wait for Kubernetes Ready")
	if drainFirst {
		steps = append(Steps("drain", "Cordon and evict pods from "+hostname), steps...)
		steps = append(steps, Step{ID: "uncordon", Title: "Mark schedulable again"})
	}
	sink.Plan(steps...)
	c, n, err := m.findNode(ctx, name, hostname)
	if err != nil {
		return err
	}
	sec, kc, err := m.clusterClients(ctx, name)
	if err != nil {
		return err
	}
	if drainFirst {
		if err := sink.Run("drain", func() error { return kc.Drain(ctx, hostname, 5*time.Minute, sinkWriter{sink, "drain", hostname}) }); err != nil {
			return err
		}
	}
	before := kubeBootID(ctx, kc, hostname)
	err = sink.Run("reboot", func() error {
		err := m.rebootAndWait(ctx, n.IP, sec.Talosconfig, func() {
			_ = m.Store.Audit(ctx, name, "node.reboot", hostname)
			sink.Emit(Info, "reboot", hostname, "reboot requested; waiting for the machine to come back")
		})
		if err != nil {
			return err
		}
		sink.Emit(Info, "reboot", hostname, "back up")
		return nil
	})
	if err != nil {
		return err
	}
	if err := sink.Run("ready", func() error { return m.waitBack(ctx, c, kc, n, before, sec.Talosconfig) }); err != nil {
		return err
	}
	if drainFirst {
		if err := sink.Run("uncordon", func() error { return kc.Uncordon(ctx, hostname) }); err != nil {
			return err
		}
	}
	sink.Emit(Done, steps[len(steps)-1].ID, hostname, "rebooted and Ready")
	return nil
}

func (m *Manager) UpgradeNode(ctx context.Context, name, hostname, version string, sink Sink) error {
	c, n, err := m.findNode(ctx, name, hostname)
	if err != nil {
		return err
	}
	if version == "" {
		version = c.Spec.TalosVersion
	}
	step := nodeStep(n)
	sink.Plan(Step{ID: step, Title: "Upgrade " + hostname + " to Talos " + version, Node: hostname})
	sec, kc, err := m.clusterClients(ctx, name)
	if err != nil {
		return err
	}
	if err := m.EnsureSchematic(ctx, c); err != nil {
		return err
	}
	want := nodeImage{version: version, schematic: c.SchematicFor(c.PoolOf(n))}
	image := m.Factory.InstallerImage(want.schematic, version)
	_ = m.Store.Audit(ctx, name, "node.upgrade", hostname+" "+version)
	return sink.Run(step, func() error {
		already, err := m.upgradeInPlace(ctx, c, kc, n, sec.Talosconfig, image, want, false, step, sink)
		if err != nil {
			return err
		}
		if already {
			sink.Emit(Done, step, hostname, "already on %s", version)
		} else {
			sink.Emit(Done, step, hostname, "on %s and Ready", version)
		}
		return nil
	})
}

type nodeImage struct {
	version   string
	schematic string
}

func (cur nodeImage) satisfies(want nodeImage, reimage bool) bool {
	if cur.version != want.version {
		return false
	}
	if cur.schematic == "" {
		return !reimage
	}
	return cur.schematic == want.schematic
}

func (cur nodeImage) schematicDiffers(desired, declared string) bool {
	if cur.schematic == "" {
		return desired != declared
	}
	return cur.schematic != desired
}

func readNodeImage(ctx context.Context, ip string, talosconfig []byte) (nodeImage, error) {
	tc, err := talos.Dial(ctx, ip, talosconfig)
	if err != nil {
		return nodeImage{}, err
	}
	defer tc.Close()
	v, err := versionTag(ctx, tc)
	if err != nil {
		return nodeImage{}, err
	}
	s, err := installedSchematic(ctx, tc)
	if err != nil {
		return nodeImage{version: v}, nil
	}
	return nodeImage{version: v, schematic: s}, nil
}

func (m *Manager) upgradeInPlace(ctx context.Context, c *config.Cluster, kc *k8s.Client, n config.Node, talosconfig []byte, image string, want nodeImage, reimage bool, step string, sink Sink) (already bool, err error) {
	if cur, err := readNodeImage(ctx, n.IP, talosconfig); err == nil && cur.satisfies(want, reimage) {
		return true, nil
	}
	return false, m.reinstall(ctx, c, kc, n, talosconfig, image, want.version, step, sink)
}

func (m *Manager) reinstall(ctx context.Context, c *config.Cluster, kc *k8s.Client, n config.Node, talosconfig []byte, image, version, step string, sink Sink) error {
	before := kubeBootID(ctx, kc, n.Hostname)
	err := m.rebootingAction(ctx, n.IP, talosconfig, func(tc *talos.Client) error {
		sink.Emit(Info, step, n.Hostname, "upgrading to %s from %s (A/B slot install, then reboot)", version, image)
		if err := upgradeNode(ctx, tc, image, m.Timeouts.Install); err != nil {
			return fmt.Errorf("upgrade: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sink.Emit(Info, step, n.Hostname, "rebooted; waiting for Ready")
	if err := m.waitBack(ctx, c, kc, n, before, talosconfig); err != nil {
		return fmt.Errorf("after upgrade: %w", err)
	}
	return nil
}

func (m *Manager) rebootAndWait(ctx context.Context, ip string, talosconfig []byte, requested func()) error {
	return m.rebootingAction(ctx, ip, talosconfig, func(tc *talos.Client) error {
		if err := rebootNode(ctx, tc); err != nil {
			return err
		}
		requested()
		return nil
	})
}

func kubeBootID(ctx context.Context, kc *k8s.Client, hostname string) string {
	ids, err := kc.NodeBootIDs(ctx, hostname)
	if err != nil {
		return ""
	}
	return ids[hostname]
}

func (m *Manager) waitBack(ctx context.Context, c *config.Cluster, kc *k8s.Client, n config.Node, kubeBoot string, talosconfig []byte) error {
	if err := kc.WaitRebooted(ctx, map[string]string{n.Hostname: kubeBoot}, m.Timeouts.Ready); err != nil {
		return err
	}
	if n.Role != config.RoleControlPlane {
		return nil
	}
	tc, err := talos.Dial(ctx, n.IP, talosconfig)
	if err != nil {
		return err
	}
	defer tc.Close()
	if err := m.waitEtcdMembers(ctx, tc, len(c.ControlPlanes())); err != nil {
		return fmt.Errorf("etcd on %s: %w", n.Hostname, err)
	}
	return nil
}

type sinkWriter struct {
	sink Sink
	step string
	node string
}

func (w sinkWriter) Write(p []byte) (int, error) {
	if s := string(p); len(s) > 1 {
		w.sink.Emit(Info, w.step, w.node, "%s", strings.TrimRight(s, "\r\n"))
	}
	return len(p), nil
}

var _ io.Writer = sinkWriter{}
