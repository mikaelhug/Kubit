package watch

import (
	"fmt"
	"slices"

	"github.com/mikaelhug/kubit/internal/cluster"
)

func Derive(name string, prev, cur *cluster.Status) []Event {
	if prev == nil {
		return nil
	}
	var out []Event
	ev := func(sev, kind, node, msg string) {
		out = append(out, Event{Cluster: name, Node: node, Severity: sev, Kind: kind, Message: msg, Notify: true})
	}
	pn := map[string]cluster.NodeStatus{}
	for _, n := range prev.Nodes {
		pn[n.Hostname] = n
	}
	for _, n := range cur.Nodes {
		p, had := pn[n.Hostname]
		if !had || !cur.APIReachable {
			continue
		}
		if n.Unschedulable != p.Unschedulable {
			if n.Unschedulable {
				ev("info", "node.cordoned", n.Hostname, fmt.Sprintf("%s cordoned", n.Hostname))
			} else {
				ev("info", "node.uncordoned", n.Hostname, fmt.Sprintf("%s uncordoned", n.Hostname))
			}
		}
		if p.TalosVersion != "" && n.TalosVersion != "" && p.TalosVersion != n.TalosVersion {
			ev("info", "talos.version", n.Hostname, fmt.Sprintf("%s: Talos %s → %s", n.Hostname, p.TalosVersion, n.TalosVersion))
		}
		if p.KubeletVersion != "" && n.KubeletVersion != "" && p.KubeletVersion != n.KubeletVersion {
			ev("info", "kubelet.version", n.Hostname, fmt.Sprintf("%s: kubelet %s → %s", n.Hostname, p.KubeletVersion, n.KubeletVersion))
		}
	}
	for _, p := range prev.Nodes {
		if !slices.ContainsFunc(cur.Nodes, func(n cluster.NodeStatus) bool { return n.Hostname == p.Hostname }) {
			ev("info", "node.removed", p.Hostname, fmt.Sprintf("%s removed from the cluster", p.Hostname))
		}
	}
	if cur.Etcd.Expected > 0 {
		if prev.Etcd.Members > 0 && prev.Etcd.Members != cur.Etcd.Members && cur.Etcd.Members > 0 {
			ev("warn", "etcd.members", "", fmt.Sprintf("etcd membership %d → %d", prev.Etcd.Members, cur.Etcd.Members))
		}
		if prev.Etcd.Leader != "" && cur.Etcd.Leader != "" && prev.Etcd.Leader != cur.Etcd.Leader {
			ev("info", "etcd.leader", "", fmt.Sprintf("etcd leader %s → %s", prev.Etcd.Leader, cur.Etcd.Leader))
		}
	}
	return out
}
