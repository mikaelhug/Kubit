package cluster

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
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

	if err := sink.Run("drain", func() error { return m.removeFromKube(ctx, name, hostname, opts.Force, sink) }); err != nil {
		return err
	}

	state := string(talos.StateMaintenance)
	err = sink.Run("reset", func() error {
		err := resetForRemoval(ctx, n, sec.Talosconfig, opts.Force, sink)
		if err == nil {
			return nil
		}
		if !opts.Force {
			return fmt.Errorf("reset %s: %w", n.IP, err)
		}
		sink.Emit(Warn, "reset", hostname, "reset failed, forgetting node anyway: %v", err)
		state = string(talos.StateConfigured)
		if n.Role == config.RoleControlPlane {
			m.evictEtcdMember(ctx, c, n, sec.Talosconfig, sink)
		}
		return nil
	})
	if err != nil {
		return err
	}

	return sink.Run("forget", func() error {
		c.Spec.Nodes = append(c.Spec.Nodes[:idx], c.Spec.Nodes[idx+1:]...)
		if err := m.saveExisting(ctx, c); err != nil {
			return err
		}
		if err := m.Store.UnassignMachine(ctx, n.MAC, n.IP, state); err != nil {
			return err
		}
		sink.Emit(Done, "forget", hostname, "removed from cluster %s", name)
		return nil
	})
}

func (m *Manager) removeFromKube(ctx context.Context, name, hostname string, force bool, sink Sink) error {
	kc, err := m.KubeClient(ctx, name)
	if err != nil {
		if !force {
			return err
		}
		sink.Emit(Warn, "drain", hostname, "skipped: %v", err)
		return nil
	}
	if force {
		pctx, cancel := context.WithTimeout(ctx, reachTimeout)
		ready, found, err := kc.NodeReady(pctx, hostname)
		cancel()
		switch {
		case err != nil:
			sink.Emit(Warn, "drain", hostname, "Kubernetes API unreachable, skipped: %v", err)
			return nil
		case !found:
			sink.Emit(Info, "drain", hostname, "not registered in Kubernetes")
			return nil
		case !ready:
			sink.Emit(Warn, "drain", hostname, "node is NotReady; deleting it without a drain")
			if err := kc.DeleteNode(ctx, hostname); err != nil {
				sink.Emit(Warn, "drain", hostname, "delete node: %v", err)
			}
			return nil
		}
	}
	sink.Emit(Info, "drain", hostname, "cordoning and draining")
	if err := kc.Drain(ctx, hostname, 5*time.Minute, io.Discard); err != nil {
		if !force {
			uctx, cancel := cleanupCtx(ctx)
			_ = kc.Uncordon(uctx, hostname)
			cancel()
			return err
		}
		sink.Emit(Warn, "drain", hostname, "drain failed, continuing because --force: %v", err)
	}
	if err := kc.DeleteNode(ctx, hostname); err != nil && !force {
		return fmt.Errorf("delete node: %w", err)
	}
	sink.Emit(Info, "drain", hostname, "removed from Kubernetes")
	return nil
}

func resetForRemoval(ctx context.Context, n config.Node, talosconfig []byte, force bool, sink Sink) error {
	if force {
		if err := talos.PortErr(ctx, n.IP, 2*time.Second); err != nil {
			return err
		}
	}
	tc, err := talos.Dial(ctx, n.IP, talosconfig)
	if err != nil {
		return err
	}
	defer tc.Close()
	sink.Emit(Info, "reset", n.Hostname, "graceful Talos reset (etcd leave, wipe, reboot to maintenance)")
	return resetNode(ctx, tc)
}

func (m *Manager) evictEtcdMember(ctx context.Context, c *config.Cluster, n config.Node, talosconfig []byte, sink Sink) {
	ips := map[string]bool{n.IP: true}
	if n.MAC != "" {
		if row, err := m.Store.GetMachine(ctx, n.MAC); err == nil && row.IP != "" {
			ips[row.IP] = true
		}
	}
	var lastErr error
	for _, cp := range c.ControlPlanes() {
		if cp.Hostname == n.Hostname {
			continue
		}
		err := evictVia(ctx, cp.IP, talosconfig, n.Hostname, ips, sink)
		if err == nil {
			return
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("no other control plane")
	}
	sink.Emit(Warn, "reset", n.Hostname, "etcd member kept (%v); if etcd has no quorum, restore a snapshot next", lastErr)
}

func evictVia(ctx context.Context, ip string, talosconfig []byte, hostname string, ips map[string]bool, sink Sink) error {
	if err := talos.PortErr(ctx, ip, 2*time.Second); err != nil {
		return err
	}
	tc, err := talos.Dial(ctx, ip, talosconfig)
	if err != nil {
		return err
	}
	defer tc.Close()
	call, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	peers, err := tc.EtcdPeers(call)
	if err != nil {
		return err
	}
	for _, p := range peers {
		if !peerMatches(p, hostname, ips) {
			continue
		}
		if err := tc.EtcdRemoveMember(call, p.ID); err != nil {
			return err
		}
		sink.Emit(Info, "reset", hostname, "etcd member %x removed via %s", p.ID, ip)
		return nil
	}
	sink.Emit(Info, "reset", hostname, "not an etcd member")
	return nil
}

func peerMatches(p talos.EtcdPeer, hostname string, ips map[string]bool) bool {
	for _, raw := range p.PeerURLs {
		if u, err := url.Parse(raw); err == nil {
			if host, _, err := net.SplitHostPort(u.Host); err == nil && ips[host] {
				return true
			}
		}
	}
	return p.Hostname == hostname
}
