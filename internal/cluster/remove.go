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
	Force bool
}

func (m *Manager) RemoveNode(ctx context.Context, name, hostname string, opts RemoveOptions, sink Sink) error {
	sink.Plan(Steps(
		"guard", "Check etcd quorum impact",
		"drain", "Cordon, drain and delete from Kubernetes",
		"reset", "Graceful Talos reset back to maintenance mode",
		"forget", "Remove from cluster.yaml",
	)...)
	c, _, err := m.LoadCluster(ctx, name)
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
	err = sink.Run("guard", func() error {
		if n.Role != config.RoleControlPlane {
			sink.Emit(Info, "guard", hostname, "worker: no quorum impact")
			return nil
		}
		remaining := len(c.ControlPlanes()) - 1
		switch {
		case remaining == 0:
			return fmt.Errorf("%s is the last control plane; removing it destroys the cluster", hostname)
		case remaining == 2 && !opts.Force:
			return fmt.Errorf("removing %s leaves 2 control planes, which cannot survive another failure; pass --force to accept", hostname)
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
	sec, err := m.Store.GetClusterSecrets(ctx, name)
	if err != nil {
		return err
	}

	err = sink.Run("drain", func() error {
		kc, err := m.KubeClient(ctx, name)
		if err != nil {
			return err
		}
		sink.Emit(Info, "drain", hostname, "cordoning and draining")
		if err := kc.Drain(ctx, hostname, 5*time.Minute, io.Discard); err != nil {
			if !opts.Force {
				return err
			}
			sink.Emit(Warn, "drain", hostname, "drain failed, continuing because --force: %v", err)
		}
		if err := kc.DeleteNode(ctx, hostname); err != nil && !opts.Force {
			return fmt.Errorf("delete node: %w", err)
		}
		sink.Emit(Info, "drain", hostname, "removed from Kubernetes")
		return nil
	})
	if err != nil {
		return err
	}

	err = sink.Run("reset", func() error {
		tc, err := talos.Dial(ctx, n.IP, sec.Talosconfig)
		if err == nil {
			sink.Emit(Info, "reset", hostname, "graceful Talos reset (etcd leave, wipe, reboot to maintenance)")
			err = resetNode(ctx, tc)
			tc.Close()
		}
		if err != nil {
			if !opts.Force {
				return fmt.Errorf("reset %s: %w", n.IP, err)
			}
			sink.Emit(Warn, "reset", hostname, "reset failed, forgetting node anyway: %v", err)
		}
		return nil
	})
	if err != nil {
		return err
	}

	return sink.Run("forget", func() error {
		c.Spec.Nodes = append(c.Spec.Nodes[:idx], c.Spec.Nodes[idx+1:]...)
		if err := m.SaveCluster(ctx, c, ""); err != nil {
			return err
		}
		if err := m.Store.UnassignNode(ctx, n.IP, string(talos.StateMaintenance)); err != nil {
			return err
		}
		_ = m.Store.Audit(ctx, name, "node.remove", marshalJSON(n))
		sink.Emit(Done, "forget", hostname, "removed from cluster %s", name)
		return nil
	})
}
