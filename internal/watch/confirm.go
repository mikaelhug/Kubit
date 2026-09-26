package watch

import (
	"fmt"
	"strings"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/store"
)

const confirmAfter = 3

var confirmedKinds = map[string]string{"talos.unreachable": "talos.back", "api.unreachable": "api.back", "etcd.unhealthy": "etcd.healthy", "node.notready": "node.ready"}

type confirm struct {
	bad  map[string]int
	open map[string]bool
}

func newConfirm() *confirm { return &confirm{bad: map[string]int{}, open: map[string]bool{}} }

func factKey(kind, node string) string { return kind + "|" + node }

func (c *confirm) Seed(open []store.EventRow) {
	for _, e := range open {
		if _, ok := confirmedKinds[e.Kind]; ok {
			c.open[factKey(e.Kind, e.Node)] = true
		}
	}
}

func (c *confirm) Reset() { c.bad = map[string]int{} }

func (c *confirm) Apply(name string, cur *cluster.Status) []store.EventRow {
	facts := badFacts(name, cur)
	var out []store.EventRow
	for key, ev := range facts {
		c.bad[key]++
		if c.bad[key] >= confirmAfter && !c.open[key] {
			c.open[key] = true
			out = append(out, ev)
		}
	}
	for key := range c.bad {
		if _, still := facts[key]; !still {
			delete(c.bad, key)
		}
	}
	for key := range c.open {
		if _, still := facts[key]; still {
			continue
		}
		delete(c.open, key)
		kind, node := splitKey(key)
		out = append(out, recovery(name, kind, node))
	}
	return out
}

func splitKey(key string) (kind, node string) {
	kind, node, _ = strings.Cut(key, "|")
	return kind, node
}

func recovery(name, kind, node string) store.EventRow {
	msg := map[string]string{"talos.back": node + ": Talos API reachable again", "api.back": "Kubernetes API reachable again", "etcd.healthy": "etcd healthy again", "node.ready": node + " is Ready"}
	rk := confirmedKinds[kind]
	return store.EventRow{Cluster: name, Node: node, Severity: "info", Kind: rk, Message: msg[rk]}
}

func badFacts(name string, cur *cluster.Status) map[string]store.EventRow {
	out := map[string]store.EventRow{}
	ev := func(sev, kind, node, msg string) {
		out[factKey(kind, node)] = store.EventRow{Cluster: name, Node: node, Severity: sev, Kind: kind, Message: msg}
	}
	for _, n := range cur.Nodes {
		if !n.TalosReachable {
			ev("critical", "talos.unreachable", n.Hostname, fmt.Sprintf("%s: Talos API unreachable (%s)", n.Hostname, n.TalosError))
		}
		if cur.APIReachable && n.Registered && !n.Ready {
			ev("warn", "node.notready", n.Hostname, fmt.Sprintf("%s is NotReady", n.Hostname))
		}
	}
	if !cur.APIReachable {
		ev("critical", "api.unreachable", "", fmt.Sprintf("Kubernetes API unreachable at %s: %s", cur.Endpoint, cur.APIError))
	}
	if cur.Etcd.Expected > 0 && !cur.Etcd.Healthy {
		ev("critical", "etcd.unhealthy", "", fmt.Sprintf("etcd unhealthy (%d/%d members)", cur.Etcd.Members, cur.Etcd.Expected))
	}
	return out
}

func unconfirmed(events []store.EventRow) []store.EventRow {
	rec := map[string]bool{}
	for _, r := range confirmedKinds {
		rec[r] = true
	}
	out := events[:0]
	for _, e := range events {
		if _, ok := confirmedKinds[e.Kind]; ok || rec[e.Kind] {
			continue
		}
		out = append(out, e)
	}
	return out
}
