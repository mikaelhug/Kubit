package cluster

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/talos"
)

type RemoveOptions struct {
	// Force skips the etcd quorum guard and tolerates an unreachable node (no drain,
	// no reset): the node is only forgotten.
	Force bool
}

// RemoveNode takes a node out of the cluster: cordon and drain, delete the Node object,
// graceful Talos reset (leaves etcd first on control planes, wipes state, reboots into
// maintenance mode), then drops it from cluster.yaml.
func (m *Manager) RemoveNode(ctx context.Context, name, hostname string, opts RemoveOptions, sink Sink) error {
	c, row, err := m.LoadCluster(ctx, name)
	if err != nil {
		return err
	}
	idx := -1
	for i, n := range c.Spec.Nodes {
		if n.Hostname == hostname {
			idx = i
		}
	}
	if idx < 0 {
		return fmt.Errorf("node %s is not part of cluster %s", hostname, name)
	}
	n := c.Spec.Nodes[idx]
	if n.Role == config.RoleControlPlane {
		remaining := len(c.ControlPlanes()) - 1
		switch {
		case remaining == 0:
			return fmt.Errorf("%s is the last control plane; removing it destroys the cluster", hostname)
		case remaining == 2 && !opts.Force:
			return fmt.Errorf("removing %s leaves 2 control planes, which cannot survive another failure; pass --force to accept", hostname)
		case remaining == 2:
			sink.emit(Warn, "remove", hostname, "cluster will run with 2 control planes: no fault tolerance")
		}
	}
	sec, err := m.Store.GetClusterSecrets(ctx, name)
	if err != nil {
		return err
	}

	kc, err := m.KubeClient(ctx, name)
	if err != nil {
		return err
	}
	sink.emit(Info, "drain", hostname, "cordoning and draining")
	if err := kc.Drain(ctx, hostname, 5*time.Minute, io.Discard); err != nil {
		if !opts.Force {
			return err
		}
		sink.emit(Warn, "drain", hostname, "drain failed, continuing because --force: %v", err)
	}
	if err := kc.DeleteNode(ctx, hostname); err != nil && !opts.Force {
		return fmt.Errorf("delete node: %w", err)
	}
	sink.emit(Info, "drain", hostname, "removed from Kubernetes")

	dial, cancel := context.WithTimeout(ctx, 30*time.Second)
	tc, err := talos.Dial(dial, n.IP, sec.Talosconfig)
	cancel()
	if err == nil {
		sink.emit(Info, "reset", hostname, "graceful Talos reset (etcd leave, wipe, reboot to maintenance)")
		err = tc.Reset(tc.Context(ctx), true, true)
		tc.Close()
	}
	if err != nil {
		if !opts.Force {
			return fmt.Errorf("reset %s: %w", n.IP, err)
		}
		sink.emit(Warn, "reset", hostname, "reset failed, forgetting node anyway: %v", err)
	}

	c.Spec.Nodes = append(c.Spec.Nodes[:idx], c.Spec.Nodes[idx+1:]...)
	if err := m.SaveCluster(ctx, c, row.State); err != nil {
		return err
	}
	if err := m.Store.UnassignNode(ctx, n.IP, string(talos.StateMaintenance)); err != nil {
		return err
	}
	_ = m.Store.Audit(ctx, name, "node.remove", marshalJSON(n))
	sink.emit(Done, "remove", hostname, "removed from cluster %s", name)
	return nil
}
