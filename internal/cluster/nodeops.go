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
		already, err := m.upgradeInPlace(ctx, c, kc, n, sec.Talosconfig, image, want, false, nodeConfigs{}, step, sink)
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

type nodeConfigs struct {
	staged []byte
	final  []byte
}

func (m *Manager) upgradeInPlace(ctx context.Context, c *config.Cluster, kc *k8s.Client, n config.Node, talosconfig []byte, image string, want nodeImage, reimage bool, cfgs nodeConfigs, step string, sink Sink) (already bool, err error) {
	if cur, err := readNodeImage(ctx, n.IP, talosconfig); err == nil && cur.satisfies(want, reimage) {
		if err := m.applyBehindAndWait(ctx, c, kc, n, cfgs.final, talosconfig, step, sink); err != nil {
			return true, err
		}
		return true, releaseCordon(ctx, kc, n.Hostname, step, sink)
	}
	return false, m.reinstallWith(ctx, c, kc, n, talosconfig, image, want.version, cfgs, step, sink)
}

func (m *Manager) reinstall(ctx context.Context, c *config.Cluster, kc *k8s.Client, n config.Node, talosconfig []byte, image, version, step string, sink Sink) error {
	return m.reinstallWith(ctx, c, kc, n, talosconfig, image, version, nodeConfigs{}, step, sink)
}

func (m *Manager) reinstallWith(ctx context.Context, c *config.Cluster, kc *k8s.Client, n config.Node, talosconfig []byte, image, version string, cfgs nodeConfigs, step string, sink Sink) error {
	if n.Role == config.RoleControlPlane {
		if err := m.etcdTolerates(ctx, c, n, talosconfig); err != nil {
			return err
		}
	}
	if err := m.installTalos(ctx, n, talosconfig, image, version, step, sink); err != nil {
		return err
	}
	pending := fmt.Sprintf("Talos %s is installed and takes effect on the next reboot", version)
	if n.Role == config.RoleControlPlane {
		if err := m.etcdTolerates(ctx, c, n, talosconfig); err != nil {
			return fmt.Errorf("%w; %s", err, pending)
		}
	}
	hold, err := holdCordon(ctx, kc, n.Hostname)
	if err != nil {
		return err
	}
	undo := func(err error) error {
		if hold == cordonUser {
			return err
		}
		uctx, cancel := cleanupCtx(ctx)
		defer cancel()
		if uerr := kc.Uncordon(uctx, n.Hostname); uerr != nil {
			sink.Emit(Warn, step, n.Hostname, "could not uncordon: %v", uerr)
		}
		return err
	}
	before := kubeBootID(ctx, kc, n.Hostname)
	if err := kc.DrainAll(ctx, n.Hostname, 5*time.Minute, sinkWriter{sink, step, n.Hostname}); err != nil {
		return undo(fmt.Errorf("%w; %s", err, pending))
	}
	rebooted, err := m.applyIfBehind(ctx, n, cfgs.staged, talosconfig, step, sink)
	if err != nil {
		err = fmt.Errorf("machine config for the new image: %w", err)
		if rebooted {
			return err
		}
		return undo(err)
	}
	if !rebooted {
		err := m.rebootAndWait(ctx, n.IP, talosconfig, func() {
			sink.Emit(Info, step, n.Hostname, "rebooting into Talos %s", version)
		})
		if err != nil {
			return fmt.Errorf("reboot: %w", err)
		}
	}
	sink.Emit(Info, step, n.Hostname, "rebooted; waiting for Ready")
	if err := m.waitBack(ctx, c, kc, n, before, talosconfig); err != nil {
		return fmt.Errorf("after upgrade: %w", err)
	}
	if err := m.applyBehindAndWait(ctx, c, kc, n, cfgs.final, talosconfig, step, sink); err != nil {
		return fmt.Errorf("machine config for Talos %s: %w", version, err)
	}
	if hold == cordonUser {
		sink.Emit(Info, step, n.Hostname, "left cordoned, as before the upgrade")
		return nil
	}
	return kc.Uncordon(ctx, n.Hostname)
}

func (m *Manager) installTalos(ctx context.Context, n config.Node, talosconfig []byte, image, version, step string, sink Sink) error {
	tc, err := talos.Dial(ctx, n.IP, talosconfig)
	if err != nil {
		return err
	}
	defer tc.Close()
	progress := func(line string) { sink.Emit(Info, step, n.Hostname, "%s", line) }
	sink.Emit(Info, step, n.Hostname, "pulling %s", image)
	name, err := pullImage(ctx, tc, image, m.Timeouts.Install, progress)
	if err != nil {
		return fmt.Errorf("pull: %w", err)
	}
	sink.Emit(Info, step, n.Hostname, "installing Talos %s into the inactive boot slot", version)
	if err := installImage(ctx, tc, name, m.Timeouts.Install, progress); err != nil {
		return fmt.Errorf("install: %w", err)
	}
	sink.Emit(Info, step, n.Hostname, "installed; draining before the reboot")
	return nil
}

const cordonByUpgrade = "upgrade"

type cordonHold int

const (
	cordonNone cordonHold = iota
	cordonUser
	cordonKubit
)

func cordonOf(unschedulable bool, by string) cordonHold {
	switch {
	case by != "":
		return cordonKubit
	case unschedulable:
		return cordonUser
	}
	return cordonNone
}

func holdCordon(ctx context.Context, kc *k8s.Client, hostname string) (cordonHold, error) {
	unschedulable, by, err := kc.CordonState(ctx, hostname)
	if err != nil {
		return cordonNone, err
	}
	if cordonOf(unschedulable, by) == cordonUser {
		return cordonUser, nil
	}
	return cordonKubit, kc.MarkCordon(ctx, hostname, cordonByUpgrade)
}

func releaseCordon(ctx context.Context, kc *k8s.Client, hostname, step string, sink Sink) error {
	unschedulable, by, err := kc.CordonState(ctx, hostname)
	if err != nil || cordonOf(unschedulable, by) != cordonKubit {
		return err
	}
	if err := kc.Uncordon(ctx, hostname); err != nil {
		return err
	}
	sink.Emit(Info, step, hostname, "uncordoned after the interrupted upgrade")
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
