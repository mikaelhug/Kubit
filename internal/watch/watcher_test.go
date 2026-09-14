package watch_test

import (
	"testing"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/watch"
)

func st(api bool, etcd int, healthy bool, nodes ...cluster.NodeStatus) *cluster.Status {
	return &cluster.Status{APIReachable: api, Etcd: cluster.EtcdStatus{Members: etcd, Expected: 3, Healthy: healthy, Leader: "a"}, Nodes: nodes, Platform: &store.PlatformStatus{Outputs: map[string]string{"ingress_ip": "10.0.0.200"}}}
}

func node(name string, talos, ready bool) cluster.NodeStatus {
	return cluster.NodeStatus{Hostname: name, TalosReachable: talos, TalosError: map[bool]string{false: "port closed"}[talos], Registered: true, Ready: ready, TalosVersion: "v1", KubeletVersion: "k1"}
}

func kinds(evs []store.EventRow) []string {
	var out []string
	for _, e := range evs {
		out = append(out, e.Severity+":"+e.Kind)
	}
	return out
}

func TestDeriveFirstObservationOnlyReportsProblems(t *testing.T) {
	healthy := st(true, 3, true, node("a", true, true), node("b", true, true))
	if evs := watch.Derive("c", nil, healthy); len(evs) != 0 {
		t.Errorf("healthy first observation must be silent, got %v", kinds(evs))
	}
	broken := st(true, 3, true, node("a", true, true), node("b", false, false))
	got := kinds(watch.Derive("c", nil, broken))
	want := []string{"critical:talos.unreachable", "warn:node.notready"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestDeriveTransitions(t *testing.T) {
	prev := st(true, 3, true, node("a", true, true), node("b", true, true))
	down := st(true, 2, true, node("a", true, true), node("b", false, true)) // kubelet grace: still Ready
	got := kinds(watch.Derive("c", prev, down))
	if len(got) != 2 || got[0] != "critical:talos.unreachable" || got[1] != "warn:etcd.members" {
		t.Errorf("node down: %v", got)
	}
	back := st(true, 3, true, node("a", true, true), node("b", true, true))
	got = kinds(watch.Derive("c", down, back))
	if len(got) != 2 || got[0] != "info:talos.back" || got[1] != "warn:etcd.members" {
		t.Errorf("node back: %v", got)
	}
	if got := kinds(watch.Derive("c", back, back)); len(got) != 0 {
		t.Errorf("no change must be silent: %v", got)
	}
}

func TestDeriveAPIAndRemoval(t *testing.T) {
	prev := st(true, 3, true, node("a", true, true), node("b", true, true))
	cur := st(false, 3, true, node("a", true, true))
	cur.Endpoint, cur.APIError = "https://x:6443", "timeout"
	got := kinds(watch.Derive("c", prev, cur))
	if len(got) != 2 || got[0] != "info:node.removed" || got[1] != "critical:api.unreachable" {
		t.Errorf("got %v", got)
	}
	cur2 := st(true, 3, true, node("a", true, true))
	cur2.Platform.Outputs["ingress_ip"] = ""
	got = kinds(watch.Derive("c", cur, cur2))
	if len(got) != 2 || got[0] != "info:api.back" || got[1] != "warn:lb.lost" {
		t.Errorf("api back / lb lost: %v", got)
	}
}
