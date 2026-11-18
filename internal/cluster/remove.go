package cluster

import (
	"context"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/mikaelhug/kubit/internal/config"
	"github.com/mikaelhug/kubit/internal/talos"
)

func (m *Manager) RemoveNode(ctx context.Context, name, hostname string, sink Sink) error {
	return m.removeNode(ctx, name, hostname, "", sink)
}

func (m *Manager) removeNode(ctx context.Context, name, hostname, successor string, sink Sink) error {
	c, _, err := m.LoadCluster(name)
	if err != nil {
		return err
	}
	idx := c.NodeIndex(hostname)
	if idx < 0 {
		return fmt.Errorf("node %s is not part of cluster %s", hostname, name)
	}
	n := c.Spec.Nodes[idx]
	handsOver := successor != "" && endpointHolder(c) == hostname
	err = sink.Run("guard", func() error {
		if err := m.quorumGuard(ctx, name, c, n, successor, sink); err != nil {
			return err
		}
		return m.holdsNoReplicas(ctx, name, hostname)
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
	if handsOver {
		s := c.Spec.Nodes[c.NodeIndex(successor)]
		at := config.EndpointAt(c.Spec.ControlPlane.Endpoint, s.IP)
		m.pinEndpoint(name, at)
		sink.Emit(Info, "drain", successor, "the API answers at %s until %s takes over %s", at, successor, n.IP)
	}
	err = sink.Run("reset", func() error {
		if err := resetForRemoval(ctx, n, sec.Talosconfig, handsOver, sink); err != nil {
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

func (m *Manager) quorumGuard(ctx context.Context, name string, c *config.Cluster, n config.Node, successor string, sink Sink) error {
	hostname := n.Hostname
	if n.Role != config.RoleControlPlane {
		sink.Emit(Info, "guard", hostname, "worker: no quorum impact")
		return nil
	}
	remaining := len(c.ControlPlanes()) - 1
	switch {
	case remaining == 0:
		return fmt.Errorf("%s is the last control plane; removing it destroys the cluster", hostname)
	case endpointHolder(c) == hostname && successor == "":
		return fmt.Errorf("%s is the API endpoint (no VIP); removing it would break every kubeconfig — set a VIP first", hostname)
	case endpointHolder(c) == hostname:
		sec, err := m.Store.GetClusterSecrets(name)
		if err != nil {
			return err
		}
		if err := m.votes(ctx, c, successor, sec.Talosconfig); err != nil {
			return err
		}
		sink.Emit(Info, "guard", hostname, "%s is a voting etcd member and takes over %s", successor, n.IP)
	case remaining == 2:
		sink.Emit(Warn, "guard", hostname, "cluster will run with 2 control planes: no fault tolerance")
	default:
		sink.Emit(Info, "guard", hostname, "%d control planes remain", remaining)
	}
	return nil
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
	if err := kc.DeleteLonghornNode(ctx, hostname); err != nil {
		sink.Emit(Warn, "drain", hostname, "Longhorn node: %v", err)
	}
	return nil
}

func resetForRemoval(ctx context.Context, n config.Node, talosconfig []byte, powerOff bool, sink Sink) error {
	tc, err := talos.Dial(ctx, n.IP, talosconfig)
	if err != nil {
		return err
	}
	defer tc.Close()
	if powerOff {
		sink.Emit(Info, "reset", n.Hostname, "graceful Talos reset (etcd leave, wipe, power off)")
		call, cancel := context.WithTimeout(ctx, resetTimeout)
		defer cancel()
		if err := tc.ResetAndPowerOff(call); err != nil && !maybeApplied(err) {
			return err
		}
		return talos.Retry(ctx, maintenanceTimeout, 5*time.Second, func() error {
			if addressInUse(ctx, n.IP, addressProbe) {
				return talos.NotReady(n.IP + " still answers")
			}
			return nil
		})
	}
	sink.Emit(Info, "reset", n.Hostname, "graceful Talos reset (etcd leave, wipe, reboot to maintenance)")
	return resetNode(ctx, tc)
}

func (m *Manager) votes(ctx context.Context, c *config.Cluster, successor string, talosconfig []byte) error {
	i := c.NodeIndex(successor)
	if i < 0 {
		return fmt.Errorf("%s is not a member", successor)
	}
	tc, err := talos.Dial(ctx, c.Spec.Nodes[i].IP, talosconfig)
	if err != nil {
		return fmt.Errorf("etcd check: %w", err)
	}
	defer tc.Close()
	members, alarms, err := etcdMembers(ctx, tc, c.ControlPlanes(), talosconfig)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(members, func(mm etcdMemberHealth) bool { return mm.name == successor && !mm.learner }) {
		return fmt.Errorf("%s is not a voting etcd member yet", successor)
	}
	return etcdHealthy(members, alarms)
}

func (m *Manager) holdsNoReplicas(ctx context.Context, name, hostname string) error {
	kc, err := m.KubeClient(ctx, name)
	if err != nil {
		return err
	}
	call, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	lh, err := kc.Longhorn(call)
	if err != nil || lh == nil {
		return err
	}
	for _, ln := range lh.Nodes {
		if ln.Name == hostname && ln.Replicas > 0 {
			return fmt.Errorf("%s still holds %d Longhorn replicas", hostname, ln.Replicas)
		}
	}
	return nil
}
