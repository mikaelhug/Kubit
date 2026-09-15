// Package watch keeps every stored cluster under observation while the daemon runs:
// it polls Status, records capacity samples, and turns state changes into events with
// a severity, so the UI has history and alerts even when nobody was looking.
package watch

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/store"
)

type Watcher struct {
	Manager  *cluster.Manager
	Store    *store.Store
	Interval time.Duration
	// ServiceInterval paces the in-cluster (workload) collection, which lists every
	// pod, service and claim; it is a multiple of Interval.
	ServiceInterval time.Duration
	// OnStatus and OnEvent feed the SSE stream; nil is allowed.
	OnStatus func(name string, st *cluster.Status)
	OnEvent  func(e store.EventRow)

	mu           sync.Mutex
	last         map[string]*cluster.Status
	lastServices map[string]*cluster.ServiceHealth
	trackers     map[string]*ServiceTracker
	running      map[string]context.CancelFunc
}

func New(m *cluster.Manager, interval time.Duration) *Watcher {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	return &Watcher{Manager: m, Store: m.Store, Interval: interval, ServiceInterval: 4 * interval, last: map[string]*cluster.Status{}, lastServices: map[string]*cluster.ServiceHealth{}, trackers: map[string]*ServiceTracker{}, running: map[string]context.CancelFunc{}}
}

// Run starts a loop per stored cluster and picks up clusters added or forgotten later.
func (w *Watcher) Run(ctx context.Context) {
	sync := func() {
		rows, err := w.Store.ListClusters(ctx)
		if err != nil {
			return
		}
		want := map[string]bool{}
		for _, r := range rows {
			if r.State == cluster.StateReady || r.State == cluster.StateBootstrapped {
				want[r.Name] = true
			}
		}
		w.mu.Lock()
		defer w.mu.Unlock()
		for name := range want {
			if _, ok := w.running[name]; !ok {
				cctx, cancel := context.WithCancel(ctx)
				w.running[name] = cancel
				go w.loop(cctx, name)
			}
		}
		for name, cancel := range w.running {
			if !want[name] {
				cancel()
				delete(w.running, name)
				delete(w.last, name)
			}
		}
	}
	sync()
	t := time.NewTicker(w.Interval)
	prune := time.NewTicker(time.Hour)
	defer t.Stop()
	defer prune.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sync()
		case <-prune.C:
			_ = w.Store.PruneSamples(ctx)
		}
	}
}

// Latest returns the most recent Status the watcher saw for a cluster.
func (w *Watcher) Latest(name string) *cluster.Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.last[name]
}

// LatestServices returns the most recent in-cluster collection for a cluster.
func (w *Watcher) LatestServices(name string) *cluster.ServiceHealth {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastServices[name]
}

func (w *Watcher) loop(ctx context.Context, name string) {
	w.tick(ctx, name)
	w.serviceTick(ctx, name)
	t := time.NewTicker(w.Interval)
	defer t.Stop()
	every := int(w.ServiceInterval / w.Interval)
	if every < 1 {
		every = 1
	}
	for i := 1; ; i++ {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.tick(ctx, name)
			if i%every == 0 {
				w.serviceTick(ctx, name)
			}
		}
	}
}

// serviceTick collects what runs in the cluster and raises/resolves workload alerts.
// It is skipped while the API server is unreachable (Status already alerts on that).
func (w *Watcher) serviceTick(ctx context.Context, name string) {
	if st := w.Latest(name); st != nil && !st.APIReachable {
		return
	}
	sh, err := w.Manager.ServiceHealth(ctx, name)
	if err != nil {
		log.Printf("watch %s: services: %v", name, err)
		return
	}
	w.mu.Lock()
	tr := w.trackers[name]
	if tr == nil {
		tr = NewServiceTracker()
		if open, err := w.Store.Events(ctx, name, 1000, true); err == nil {
			tr.Seed(open)
		}
		w.trackers[name] = tr
	}
	w.lastServices[name] = sh
	w.mu.Unlock()
	var ignore []string
	if set, err := w.Store.GetSettings(ctx); err == nil {
		ignore = set.Alerts.IgnoreNamespaces
	}
	w.emit(ctx, name, tr.Derive(name, sh, time.Now(), ignore))
}

// emit records events, auto-resolving the alert a recovery clears, and pushes them.
func (w *Watcher) emit(ctx context.Context, name string, events []store.EventRow) {
	now := time.Now()
	for _, e := range events {
		if resolves, ok := resolves[e.Kind]; ok {
			_ = w.Store.ResolveEvents(ctx, name, e.Node, resolves)
		}
		// A daemon restart observes the same bad facts again; one open alert per
		// (object, kind) is enough for the UI and the forwarders.
		if e.Severity != "info" && w.Store.HasOpenEvent(ctx, name, e.Node, e.Kind) {
			continue
		}
		if id, err := w.Store.AddEvent(ctx, e); err == nil {
			e.ID = id
			e.TS = now.UTC().Format(time.RFC3339)
			if w.OnEvent != nil {
				w.OnEvent(e)
			}
		}
	}
}

func (w *Watcher) tick(ctx context.Context, name string) {
	st, err := w.Manager.Status(ctx, name)
	if err != nil {
		log.Printf("watch %s: %v", name, err)
		return
	}
	w.mu.Lock()
	prev := w.last[name]
	w.last[name] = st
	w.mu.Unlock()

	now := time.Now()
	samples := []store.Sample{{CPUMilli: st.Totals.CPUMilli, CPUCap: st.Totals.CPUCapMilli, MemBytes: st.Totals.MemBytes, MemCap: st.Totals.MemCapBytes, Pods: st.Totals.Pods, Ready: st.Totals.NodesReady == st.Totals.Nodes, Reachable: st.APIReachable}}
	for _, n := range st.Nodes {
		samples = append(samples, store.Sample{Node: n.Hostname, CPUMilli: n.CPUMilli, CPUCap: n.CPUCapMilli, MemBytes: n.MemBytes, MemCap: n.MemCapBytes, Pods: n.Pods, Ready: n.Ready, Reachable: n.TalosReachable})
	}
	_ = w.Store.AddSamples(ctx, name, now, samples)

	events := Derive(name, prev, st)
	if prev == nil {
		events = append(events, w.reconcileOpen(ctx, name, st)...)
	}
	w.emit(ctx, name, events)
	if w.OnStatus != nil {
		w.OnStatus(name, st)
	}
}

// reconcileOpen closes alerts left open from before a daemon restart whose condition
// no longer holds: Derive only reports transitions, so without this a transient
// failure observed right before a restart would stay "active" for ever.
func (w *Watcher) reconcileOpen(ctx context.Context, name string, st *cluster.Status) []store.EventRow {
	var out []store.EventRow
	rec := func(kind, node, msg string) {
		if alert := resolves[kind]; alert != "" && w.Store.HasOpenEvent(ctx, name, node, alert) {
			out = append(out, store.EventRow{Cluster: name, Node: node, Severity: "info", Kind: kind, Message: msg})
		}
	}
	for _, n := range st.Nodes {
		if n.TalosReachable {
			rec("talos.back", n.Hostname, n.Hostname+": Talos API reachable")
		}
		if st.APIReachable && n.Ready {
			rec("node.ready", n.Hostname, n.Hostname+" is Ready")
		}
	}
	if st.APIReachable {
		rec("api.back", "", "Kubernetes API reachable")
	}
	if st.Etcd.Healthy {
		rec("etcd.healthy", "", "etcd healthy")
	}
	if st.Platform != nil && st.Platform.Outputs["ingress_ip"] != "" {
		rec("lb.assigned", "", "ingress LoadBalancer IP "+st.Platform.Outputs["ingress_ip"])
	}
	return out
}

// resolves maps a recovery event to the alert kind it clears.
var resolves = map[string]string{
	"talos.back": "talos.unreachable", "node.ready": "node.notready", "api.back": "api.unreachable", "etcd.healthy": "etcd.unhealthy", "lb.assigned": "lb.lost",
}

// Derive compares two consecutive statuses and returns the events describing what
// changed. A nil prev yields only "currently bad" facts so a restart of the daemon
// does not replay history but still surfaces an unhealthy cluster.
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
