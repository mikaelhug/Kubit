package watch

import (
	"fmt"
	"strings"

	"github.com/mikael/kubit/internal/cluster"
)

const confirmAfter = 3

var confirmedKinds = map[string]string{"talos.unreachable": "talos.back", "api.unreachable": "api.back", "etcd.unhealthy": "etcd.healthy", "node.notready": "node.ready"}

type confirm struct {
	started bool
	bad     map[string]int
	open    map[string]bool
	quiet   map[string]bool
}

type transition struct {
	raise    *Event
	resolves string
	recovery Event
}

func newConfirm() *confirm {
	return &confirm{bad: map[string]int{}, open: map[string]bool{}, quiet: map[string]bool{}}
}

func factKey(kind, node string) string { return kind + "|" + node }

func (c *confirm) Seed(open []Event) {
	c.started = true
	for _, e := range open {
		if _, ok := confirmedKinds[e.Kind]; ok {
			c.open[factKey(e.Kind, e.Node)] = true
		}
	}
}

func (c *confirm) Reset() { c.bad = map[string]int{} }

func (c *confirm) Apply(name string, facts map[string]Event) []transition {
	if !c.started {
		c.started = true
		for key := range facts {
			c.quiet[key] = true
		}
	}
	var out []transition
	for key, ev := range facts {
		c.bad[key]++
		if c.bad[key] >= confirmAfter && !c.open[key] {
			c.open[key] = true
			ev.Notify = !c.quiet[key]
			out = append(out, transition{raise: &ev})
		}
	}
	for key := range c.bad {
		if _, still := facts[key]; !still {
			delete(c.bad, key)
			delete(c.quiet, key)
		}
	}
	for key := range c.open {
		if _, still := facts[key]; still {
			continue
		}
		delete(c.open, key)
		kind, node := splitKey(key)
		out = append(out, transition{resolves: kind, recovery: recovery(name, kind, node)})
	}
	return out
}

func splitKey(key string) (kind, node string) {
	kind, node, _ = strings.Cut(key, "|")
	return kind, node
}

func recovery(name, kind, node string) Event {
	msg := map[string]string{"talos.back": node + ": Talos API reachable again", "api.back": "Kubernetes API reachable again", "etcd.healthy": "etcd healthy again", "node.ready": node + " is Ready"}
	rk := confirmedKinds[kind]
	return Event{Cluster: name, Node: node, Severity: "info", Kind: rk, Message: msg[rk]}
}

func badFacts(name string, cur *cluster.Status) map[string]Event {
	out := map[string]Event{}
	ev := func(sev, kind, node, msg string) {
		out[factKey(kind, node)] = Event{Cluster: name, Node: node, Severity: sev, Kind: kind, Message: msg}
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
