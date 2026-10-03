package watch

import (
	"slices"
	"testing"

	"github.com/mikael/kubit/internal/cluster"
)

func TestKubeScopesAddsAddonsForAddonNamespaces(t *testing.T) {
	for _, c := range []struct {
		scope, ns string
		want      []string
	}{
		{"workloads", "metallb-system", []string{"workloads", "addons"}},
		{"workloads", "longhorn-system", []string{"workloads", "addons"}},
		{"workloads", "kube-system", []string{"workloads", "addons"}},
		{"workloads", "flux-system", []string{"workloads", "addons"}},
		{"workloads", "kubit-builds", []string{"workloads", "addons"}},
		{"workloads", "kube-public", []string{"workloads"}},
		{"workloads", "default", []string{"workloads"}},
		{"workloads", "", []string{"workloads"}},
		{"workloads", "traefik", []string{"workloads", "addons"}},
		{"network", "traefik", []string{"network"}},
		{"nodes", "", []string{"nodes"}},
	} {
		if got := kubeScopes(c.scope, c.ns); !slices.Equal(got, c.want) {
			t.Errorf("kubeScopes(%q, %q) = %v, want %v", c.scope, c.ns, got, c.want)
		}
	}
}

func TestTalosChangesRefreshNodes(t *testing.T) {
	node := func(host, version, stage string, reachable bool) cluster.NodeStatus {
		return cluster.NodeStatus{Hostname: host, TalosVersion: version, Stage: stage, TalosReachable: reachable}
	}
	status := func(nodes ...cluster.NodeStatus) *cluster.Status { return &cluster.Status{Nodes: nodes} }
	base := status(node("a", "v1.14.0", "running", true), node("b", "v1.14.0", "running", true))
	for _, c := range []struct {
		name      string
		prev, cur *cluster.Status
		want      bool
	}{
		{"first tick", nil, base, false},
		{"unchanged", base, status(node("a", "v1.14.0", "running", true), node("b", "v1.14.0", "running", true)), false},
		{"metrics only", base, status(node("a", "v1.14.0", "running", true), cluster.NodeStatus{Hostname: "b", TalosVersion: "v1.14.0", Stage: "running", TalosReachable: true, CPUMilli: 900}), false},
		{"stage", base, status(node("a", "v1.14.0", "rebooting", true), node("b", "v1.14.0", "running", true)), true},
		{"version", base, status(node("a", "v1.14.0", "running", true), node("b", "v1.14.1", "running", true)), true},
		{"reachability", base, status(node("a", "v1.14.0", "running", false), node("b", "v1.14.0", "running", true)), true},
		{"node added", base, status(node("a", "v1.14.0", "running", true), node("b", "v1.14.0", "running", true), node("c", "v1.14.0", "running", true)), true},
		{"node renamed", base, status(node("a", "v1.14.0", "running", true), node("z", "v1.14.0", "running", true)), true},
	} {
		if got := talosChanged(c.prev, c.cur); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}
