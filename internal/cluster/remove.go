package cluster

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/talos"
)

func (m *Manager) RemoveNode(ctx context.Context, name, hostname string, sink Sink) error {
	c, _, err := m.LoadCluster(name)
	if err != nil {
		return err
	}
	idx := c.NodeIndex(hostname)
	if idx < 0 {
		return fmt.Errorf("node %s is not part of cluster %s", hostname, name)
	}
	n := c.Spec.Nodes[idx]
	err = sink.Run("guard", func() error {
		if n.Role != config.RoleControlPlane {
			sink.Emit(Info, "guard", hostname, "worker: no quorum impact")
			return nil
		}
		remaining := len(c.ControlPlanes()) - 1
		ep, isEndpoint := c.EndpointNode()
		switch {
		case remaining == 0:
			return fmt.Errorf("%s is the last control plane; removing it destroys the cluster", hostname)
		case isEndpoint && ep.Hostname == hostname:
			return fmt.Errorf("%s is the API endpoint (no VIP); removing it would break every kubeconfig — set a VIP first", hostname)
		case remaining == 2:
			sink.Emit(Warn, "guard", hostname, "cluster will run with 2 control planes: no fault tolerance")
		default:
			sink.Emit(Info, "guard", hostname, "%d control planes remain", remaining)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sec, err := m.Store.GetClusterSecrets(name)
	if err != nil {
		return err
	}

	if err := sink.Run("drain", func() error { return m.removeFromKube(ctx, name, hostname, sink) }); err != nil {
		return err
	}
	err = sink.Run("reset", func() error {
		if err := resetForRemoval(ctx, n, sec.Talosconfig, sink); err != nil {
			return fmt.Errorf("reset %s: %w", n.IP, err)
		}
		return nil
	})
	if err != nil {
		return err
	}

	return sink.Run("forget", func() error {
		c.Spec.Nodes = append(c.Spec.Nodes[:idx], c.Spec.Nodes[idx+1:]...)
		m.saveCluster(c, "")
		m.Store.UnassignMachine(n.MAC, n.IP, string(talos.StateMaintenance))
		sink.Emit(Done, "forget", hostname, "removed from cluster %s", name)
		return nil
	})
}

func (m *Manager) removeFromKube(ctx context.Context, name, hostname string, sink Sink) error {
	kc, err := m.KubeClient(ctx, name)
	if err != nil {
		return err
	}
	sink.Emit(Info, "drain", hostname, "cordoning and draining")
	if err := kc.Drain(ctx, hostname, 5*time.Minute, io.Discard); err != nil {
		uctx, cancel := cleanupCtx(ctx)
		_ = kc.Uncordon(uctx, hostname)
		cancel()
		return err
	}
	if err := kc.DeleteNode(ctx, hostname); err != nil {
		return fmt.Errorf("delete node: %w", err)
	}
	sink.Emit(Info, "drain", hostname, "removed from Kubernetes")
	return nil
}

func resetForRemoval(ctx context.Context, n config.Node, talosconfig []byte, sink Sink) error {
	tc, err := talos.Dial(ctx, n.IP, talosconfig)
	if err != nil {
		return err
	}
	defer tc.Close()
	sink.Emit(Info, "reset", n.Hostname, "graceful Talos reset (etcd leave, wipe, reboot to maintenance)")
	return resetNode(ctx, tc)
}
