package watch

import (
	"fmt"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/store"
)

var resolves = map[string]string{
	"talos.back": "talos.unreachable", "node.ready": "node.notready", "node.memory-ok": "node.memory-small", "api.back": "api.unreachable", "etcd.healthy": "etcd.unhealthy", "lb.assigned": "lb.lost",
	"labhost.back": "labhost.unreachable", "labhost.disk-ok": "labhost.disk-low", "labhost.memory-ok": "labhost.memory-pressure",
	"workload.available": "workload.unavailable", "pod.recovered": "pod.crashloop", "pvc.bound": "pvc.pending", "service.endpoints": "service.no-endpoints",
	"ingress.address": "ingress.no-address", "lb.pool-free": "lb.pool-exhausted", "flux.ready": "flux.not-ready",
}

const minAllocatableBytes = 768 << 20

func Derive(name string, prev, cur *cluster.Status) []store.EventRow {
	var out []store.EventRow
	ev := func(sev, kind, node, msg string) {
		out = append(out, store.EventRow{Cluster: name, Node: node, Severity: sev, Kind: kind, Message: msg})
	}
	pn := map[string]cluster.NodeStatus{}
	if prev != nil {
		for _, n := range prev.Nodes {
			pn[n.Hostname] = n
		}
	}
	for _, n := range cur.Nodes {
		p, had := pn[n.Hostname]
		switch {
		case !n.TalosReachable && (prev == nil || (had && p.TalosReachable)):
			ev("critical", "talos.unreachable", n.Hostname, fmt.Sprintf("%s: Talos API unreachable (%s)", n.Hostname, n.TalosError))
		case n.TalosReachable && had && !p.TalosReachable:
			ev("info", "talos.back", n.Hostname, fmt.Sprintf("%s: Talos API reachable again", n.Hostname))
		}
		if n.SeenAt != "" && (prev == nil || !had || p.SeenAt != n.SeenAt) {
			ev("warn", "machine.ip-changed", n.Hostname, fmt.Sprintf("%s is declared at %s but was last seen at %s; update its address", n.Hostname, n.IP, n.SeenAt))
		}
		if cur.APIReachable {
			switch {
			case !n.Ready && n.Registered && (prev == nil || (had && p.Ready)):
				ev("warn", "node.notready", n.Hostname, fmt.Sprintf("%s is NotReady", n.Hostname))
			case n.Ready && had && !p.Ready && prev.APIReachable:
				ev("info", "node.ready", n.Hostname, fmt.Sprintf("%s is Ready", n.Hostname))
			}
			if had && n.Unschedulable != p.Unschedulable {
				if n.Unschedulable {
					ev("info", "node.cordoned", n.Hostname, fmt.Sprintf("%s cordoned", n.Hostname))
				} else {
					ev("info", "node.uncordoned", n.Hostname, fmt.Sprintf("%s uncordoned", n.Hostname))
				}
			}
			if had && p.TalosVersion != "" && n.TalosVersion != "" && p.TalosVersion != n.TalosVersion {
				ev("info", "talos.version", n.Hostname, fmt.Sprintf("%s: Talos %s → %s", n.Hostname, p.TalosVersion, n.TalosVersion))
			}
			small := n.Registered && n.MemAllocBytes > 0 && n.MemAllocBytes < minAllocatableBytes
			wasSmall := had && p.Registered && p.MemAllocBytes > 0 && p.MemAllocBytes < minAllocatableBytes
			switch {
			case small && (prev == nil || !wasSmall):
				ev("warn", "node.memory-small", n.Hostname, fmt.Sprintf("%s has %d MiB allocatable for pods; the platform add-ons alone need more. Give it at least 2 GiB.", n.Hostname, n.MemAllocBytes>>20))
			case !small && wasSmall:
				ev("info", "node.memory-ok", n.Hostname, fmt.Sprintf("%s has %d MiB allocatable for pods", n.Hostname, n.MemAllocBytes>>20))
			}
			if had && p.KubeletVersion != "" && n.KubeletVersion != "" && p.KubeletVersion != n.KubeletVersion {
				ev("info", "kubelet.version", n.Hostname, fmt.Sprintf("%s: kubelet %s → %s", n.Hostname, p.KubeletVersion, n.KubeletVersion))
			}
		}
	}
	if prev != nil {
		for _, p := range prev.Nodes {
			found := false
			for _, n := range cur.Nodes {
				if n.Hostname == p.Hostname {
					found = true
				}
			}
			if !found {
				ev("info", "node.removed", p.Hostname, fmt.Sprintf("%s removed from the cluster", p.Hostname))
			}
		}
	}
	switch {
	case !cur.APIReachable && (prev == nil || prev.APIReachable):
		ev("critical", "api.unreachable", "", fmt.Sprintf("Kubernetes API unreachable at %s: %s", cur.Endpoint, cur.APIError))
	case cur.APIReachable && prev != nil && !prev.APIReachable:
		ev("info", "api.back", "", "Kubernetes API reachable again")
	}
	if cur.Etcd.Expected > 0 {
		switch {
		case !cur.Etcd.Healthy && (prev == nil || prev.Etcd.Healthy):
			ev("critical", "etcd.unhealthy", "", fmt.Sprintf("etcd unhealthy (%d/%d members)", cur.Etcd.Members, cur.Etcd.Expected))
		case cur.Etcd.Healthy && prev != nil && !prev.Etcd.Healthy:
			ev("info", "etcd.healthy", "", "etcd healthy again")
		}
		if prev != nil && prev.Etcd.Members > 0 && prev.Etcd.Members != cur.Etcd.Members && cur.Etcd.Members > 0 {
			ev("warn", "etcd.members", "", fmt.Sprintf("etcd membership %d → %d", prev.Etcd.Members, cur.Etcd.Members))
		}
		if prev != nil && prev.Etcd.Leader != "" && cur.Etcd.Leader != "" && prev.Etcd.Leader != cur.Etcd.Leader {
			ev("info", "etcd.leader", "", fmt.Sprintf("etcd leader %s → %s", prev.Etcd.Leader, cur.Etcd.Leader))
		}
	}
	pIP, cIP := "", ""
	if prev != nil && prev.Platform != nil {
		pIP = prev.Platform.Outputs["ingress_ip"]
	}
	if cur.Platform != nil {
		cIP = cur.Platform.Outputs["ingress_ip"]
	}
	if prev != nil && pIP != cIP {
		if cIP == "" {
			ev("warn", "lb.lost", "", fmt.Sprintf("ingress LoadBalancer IP %s released", pIP))
		} else {
			ev("info", "lb.assigned", "", fmt.Sprintf("ingress LoadBalancer IP %s", cIP))
		}
	}
	return out
}
