package cluster

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/talos"
)

func (m *Manager) findNode(ctx context.Context, name, hostname string) (*config.Cluster, config.Node, error) {
	c, _, err := m.LoadCluster(ctx, name)
	if err != nil {
		return nil, config.Node{}, err
	}
	for _, n := range c.Spec.Nodes {
		if n.Hostname == hostname {
			return c, n, nil
		}
	}
	return nil, config.Node{}, fmt.Errorf("node %s is not part of cluster %s", hostname, name)
}

// CordonNode marks the node unschedulable; running pods stay.
func (m *Manager) CordonNode(ctx context.Context, name, hostname string, sink Sink) error {
	sink.plan(Steps("cordon", "Mark "+hostname+" unschedulable")...)
	if _, _, err := m.findNode(ctx, name, hostname); err != nil {
		return err
	}
	return sink.run("cordon", func() error {
		kc, err := m.KubeClient(ctx, name)
		if err != nil {
			return err
		}
		if err := kc.Cordon(ctx, hostname); err != nil {
			return err
		}
		_ = m.Store.Audit(ctx, name, "node.cordon", hostname)
		sink.emit(Done, "cordon", hostname, "cordoned; new pods will not be scheduled here")
		return nil
	})
}

func (m *Manager) UncordonNode(ctx context.Context, name, hostname string, sink Sink) error {
	sink.plan(Steps("uncordon", "Mark "+hostname+" schedulable")...)
	if _, _, err := m.findNode(ctx, name, hostname); err != nil {
		return err
	}
	return sink.run("uncordon", func() error {
		kc, err := m.KubeClient(ctx, name)
		if err != nil {
			return err
		}
		if err := kc.Uncordon(ctx, hostname); err != nil {
			return err
		}
		_ = m.Store.Audit(ctx, name, "node.uncordon", hostname)
		sink.emit(Done, "uncordon", hostname, "schedulable again")
		return nil
	})
}

// DrainNode cordons and evicts pods (DaemonSets stay); the node remains in the cluster.
func (m *Manager) DrainNode(ctx context.Context, name, hostname string, sink Sink) error {
	sink.plan(Steps("drain", "Cordon and evict pods from "+hostname)...)
	if _, _, err := m.findNode(ctx, name, hostname); err != nil {
		return err
	}
	return sink.run("drain", func() error {
		kc, err := m.KubeClient(ctx, name)
		if err != nil {
			return err
		}
		if err := kc.Drain(ctx, hostname, 5*time.Minute, sinkWriter{sink, "drain", hostname}); err != nil {
			return err
		}
		_ = m.Store.Audit(ctx, name, "node.drain", hostname)
		sink.emit(Done, "drain", hostname, "drained; uncordon to schedule pods here again")
		return nil
	})
}

// RebootNode optionally drains first, reboots through the Talos API, waits for the
// machine to come back and, if it was drained, uncordons it.
func (m *Manager) RebootNode(ctx context.Context, name, hostname string, drainFirst bool, sink Sink) error {
	steps := Steps("reboot", "Reboot "+hostname+" and wait for it to return", "ready", "Wait for Kubernetes Ready")
	if drainFirst {
		steps = append(Steps("drain", "Cordon and evict pods from "+hostname), steps...)
		steps = append(steps, Step{ID: "uncordon", Title: "Mark schedulable again"})
	}
	sink.plan(steps...)
	c, n, err := m.findNode(ctx, name, hostname)
	if err != nil {
		return err
	}
	sec, err := m.Store.GetClusterSecrets(ctx, name)
	if err != nil {
		return err
	}
	kc, err := m.KubeClient(ctx, name)
	if err != nil {
		return err
	}
	if drainFirst {
		if err := sink.run("drain", func() error { return kc.Drain(ctx, hostname, 5*time.Minute, sinkWriter{sink, "drain", hostname}) }); err != nil {
			return err
		}
	}
	err = sink.run("reboot", func() error {
		dial, cancel := context.WithTimeout(ctx, 30*time.Second)
		tc, err := talos.Dial(dial, n.IP, sec.Talosconfig)
		cancel()
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
		_ = m.Store.Audit(ctx, name, "node.reboot", hostname)
		sink.emit(Info, "reboot", hostname, "reboot requested; waiting for the machine to come back")
		if err := talos.WaitForReboot(ctx, n.IP, sec.Talosconfig, bootID, m.Timeouts.Install); err != nil {
			return err
		}
		sink.emit(Info, "reboot", hostname, "back up")
		return nil
	})
	if err != nil {
		return err
	}
	if err := sink.run("ready", func() error { return kc.WaitReady(ctx, []string{hostname}, m.Timeouts.Ready, nil) }); err != nil {
		return err
	}
	if drainFirst {
		if err := sink.run("uncordon", func() error { return kc.Uncordon(ctx, hostname) }); err != nil {
			return err
		}
	}
	_ = c
	sink.emit(Done, steps[len(steps)-1].ID, hostname, "rebooted and Ready")
	return nil
}

// UpgradeNode upgrades one node's Talos to the cluster's declared version (or a given
// one), the same way the rolling upgrade treats each node.
func (m *Manager) UpgradeNode(ctx context.Context, name, hostname, version string, sink Sink) error {
	c, n, err := m.findNode(ctx, name, hostname)
	if err != nil {
		return err
	}
	if version == "" {
		version = c.Spec.TalosVersion
	}
	step := nodeStep(n)
	sink.plan(Step{ID: step, Title: "Upgrade " + hostname + " to Talos " + version, Node: hostname})
	sec, err := m.Store.GetClusterSecrets(ctx, name)
	if err != nil {
		return err
	}
	kc, err := m.KubeClient(ctx, name)
	if err != nil {
		return err
	}
	image := m.Factory.InstallerImage(c.Spec.SchematicID, version)
	_ = m.Store.Audit(ctx, name, "node.upgrade", hostname+" "+version)
	return sink.run(step, func() error {
		dial, cancel := context.WithTimeout(ctx, 30*time.Second)
		tc, err := talos.Dial(dial, n.IP, sec.Talosconfig)
		cancel()
		if err != nil {
			return err
		}
		v, err := tc.Version(tc.Context(ctx))
		if err == nil && len(v.Messages) > 0 && v.Messages[0].Version.Tag == version {
			tc.Close()
			sink.emit(Done, step, hostname, "already on %s", version)
			return nil
		}
		bootID, err := tc.BootID(ctx)
		if err != nil {
			tc.Close()
			return err
		}
		sink.emit(Info, step, hostname, "upgrading to %s using %s", version, image)
		_, err = tc.Upgrade(tc.Context(ctx), image, false, false)
		tc.Close()
		if err != nil {
			return fmt.Errorf("upgrade: %w", err)
		}
		if err := talos.WaitForReboot(ctx, n.IP, sec.Talosconfig, bootID, m.Timeouts.Install); err != nil {
			return err
		}
		sink.emit(Info, step, hostname, "rebooted; waiting for Ready")
		if err := kc.WaitReady(ctx, []string{hostname}, m.Timeouts.Ready, nil); err != nil {
			return err
		}
		sink.emit(Done, step, hostname, "on %s and Ready", version)
		return nil
	})
}

// sinkWriter forwards kubectl drain's progress lines as events.
type sinkWriter struct {
	sink Sink
	step string
	node string
}

func (w sinkWriter) Write(p []byte) (int, error) {
	if s := string(p); len(s) > 1 {
		w.sink.emit(Info, w.step, w.node, "%s", trimNL(s))
	}
	return len(p), nil
}

func trimNL(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

var _ io.Writer = sinkWriter{}
