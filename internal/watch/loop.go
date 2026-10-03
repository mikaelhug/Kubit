package watch

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"reflect"
	"slices"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/k8s"
	"github.com/mikael/kubit/internal/store"
)

func (w *Watcher) loop(ctx context.Context, name string) {
	kube := make(chan struct{})
	go func() {
		defer close(kube)
		w.watchKubernetes(ctx, name)
	}()
	defer func() { <-kube }()
	w.tick(ctx, name)
	w.serviceTick(ctx, name)
	t := time.NewTicker(w.Interval())
	defer t.Stop()
	every := w.serviceEvery()
	retune := w.retuned()
	for i := 1; ; {
		select {
		case <-ctx.Done():
			return
		case <-retune:
			retune = w.retuned()
			t.Reset(w.Interval())
			every = w.serviceEvery()
		case <-t.C:
			w.tick(ctx, name)
			if i%every == 0 {
				w.serviceTick(ctx, name)
			}
			i++
		}
	}
}

func (w *Watcher) serviceEvery() int {
	return max(int(w.ServiceInterval/w.Interval()), 1)
}

func (w *Watcher) watchKubernetes(ctx context.Context, name string) {
	deb := k8s.NewDebouncer(300*time.Millisecond, time.Second, func(scope string) { w.refresh(name, scope) })
	defer deb.Stop()
	changed := w.kubeSignal(name)
	defer w.dropKubeSignal(name, changed)
	backoff := 5 * time.Second
	for ctx.Err() == nil {
		if !informable(w.Latest(name)) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			continue
		}
		rotated := false
		kc, kubeconfig, err := w.kubeClient(ctx, name)
		if err != nil {
			log.Printf("watch %s: informers: %v", name, err)
		} else {
			started := time.Now()
			rotated = w.runInformers(ctx, name, kc, kubeconfig, changed, deb.Hit)
			if rotated || time.Since(started) > time.Minute {
				backoff = 5 * time.Second
			}
		}
		if rotated {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < time.Minute {
			backoff *= 2
		}
	}
}

func (w *Watcher) kubeClient(ctx context.Context, name string) (*k8s.Client, []byte, error) {
	sec, err := w.Store.GetClusterSecrets(ctx, name)
	if err != nil {
		return nil, nil, err
	}
	kc, err := w.Manager.KubeClientFor(name, sec)
	return kc, sec.Kubeconfig, err
}

func (w *Watcher) runInformers(ctx context.Context, name string, kc *k8s.Client, kubeconfig []byte, changed <-chan struct{}, hit func(string)) (rotated bool) {
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		kc.WatchScopes(wctx, func(scope, namespace string) {
			for _, sc := range kubeScopes(scope, namespace) {
				hit(sc)
			}
		})
	}()
	for {
		select {
		case <-done:
			return false
		case <-changed:
			if !informable(w.Latest(name)) {
				log.Printf("watch %s: Kubernetes API unreachable; dropping informer cache", name)
				cancel()
				<-done
				return false
			}
			sec, err := w.Store.GetClusterSecrets(ctx, name)
			if err != nil || bytes.Equal(sec.Kubeconfig, kubeconfig) {
				continue
			}
			log.Printf("watch %s: kubeconfig changed; reconnecting informers", name)
			cancel()
			<-done
			return true
		}
	}
}

func informable(st *cluster.Status) bool {
	return st != nil && st.APIReachable && cluster.Observable(st.State)
}

func kubeScopes(scope, namespace string) []string {
	if scope == k8s.ScopeWorkloads {
		if cluster.AddonNamespace(namespace) {
			return []string{scope, k8s.ScopeAddons}
		}
	}
	return []string{scope}
}

func (w *Watcher) refresh(name, scope string) {
	if w.OnRefresh != nil {
		w.OnRefresh(name, scope)
	}
}

func (w *Watcher) kubeSignal(name string) chan struct{} {
	w.sigMu.Lock()
	defer w.sigMu.Unlock()
	ch := make(chan struct{}, 1)
	w.kubeSignals[name] = ch
	return ch
}

func (w *Watcher) dropKubeSignal(name string, ch chan struct{}) {
	w.sigMu.Lock()
	defer w.sigMu.Unlock()
	if w.kubeSignals[name] == ch {
		delete(w.kubeSignals, name)
	}
}

func (w *Watcher) onStoreChange(c store.Change) {
	if c.Table != "secrets" && c.Table != "*" {
		return
	}
	w.sigMu.Lock()
	defer w.sigMu.Unlock()
	for name, ch := range w.kubeSignals {
		if c.Table == "*" || c.Cluster == name {
			nudge(ch)
		}
	}
}

func (w *Watcher) signalKube(name string) {
	w.sigMu.Lock()
	defer w.sigMu.Unlock()
	if ch := w.kubeSignals[name]; ch != nil {
		nudge(ch)
	}
}

func nudge(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (w *Watcher) every(ctx context.Context, d time.Duration, tick func(context.Context)) {
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

var disruptive = []string{"cluster.create", "cluster.apply", "etcd.restore", "upgrade.talos", "upgrade.kubernetes", "node.add", "node.remove", "node.reboot", "node.rename", "node.pool", "node.readdress", "node.upgrade", "platform.apply", "labhost.update", "labhost.reboot"}

const quietAfterOperation = 10 * time.Minute

const serviceTickTimeout = 45 * time.Second

func (w *Watcher) serviceTick(ctx context.Context, name string) {
	if st := w.Latest(name); st == nil || !st.APIReachable || st.State != cluster.StateReady {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, serviceTickTimeout)
	defer cancel()
	sh, err := w.Manager.ServiceHealth(ctx, name)
	if err != nil {
		log.Printf("watch %s: services: %v", name, err)
		return
	}
	w.mu.Lock()
	changed := !sameServices(w.lastServices[name], sh)
	w.lastServices[name] = sh
	w.mu.Unlock()
	if changed {
		w.refresh(name, k8s.ScopeServices)
	}
	if last := w.Store.LastFinished(ctx, name, disruptive); time.Since(last) < quietAfterOperation {
		return
	}
	w.mu.Lock()
	tr := w.trackers[name]
	w.mu.Unlock()
	if tr == nil {
		fresh := newServiceTracker()
		if open, err := w.Store.OpenAlerts(ctx, name); err == nil {
			fresh.Seed(open)
		}
		w.mu.Lock()
		if tr = w.trackers[name]; tr == nil {
			tr = fresh
			w.trackers[name] = tr
		}
		w.mu.Unlock()
	}
	var ignore []string
	if set, err := w.Store.GetSettings(ctx); err == nil {
		ignore = set.Alerts.IgnoreNamespaces
	}
	w.emit(ctx, name, tr.Derive(name, sh, time.Now(), ignore))
}

func sameServices(a, b *cluster.ServiceHealth) bool {
	if a == nil || b == nil {
		return a == b
	}
	return reflect.DeepEqual(ageless(a), ageless(b))
}

func ageless(sh *cluster.ServiceHealth) cluster.ServiceHealth {
	out := *sh
	out.CollectedAt = time.Time{}
	out.Workloads = zeroed(sh.Workloads, func(w *cluster.WorkloadHealth) { w.AgeSec = 0 })
	out.Pods = zeroed(sh.Pods, func(p *cluster.PodHealth) { p.AgeSec = 0 })
	out.Claims = zeroed(sh.Claims, func(c *cluster.ClaimHealth) { c.AgeSec = 0 })
	out.Services = zeroed(sh.Services, func(s *cluster.ServiceRow) { s.AgeSec = 0 })
	out.Ingresses = zeroed(sh.Ingresses, func(i *cluster.IngressHealth) { i.AgeSec = 0 })
	return out
}

func zeroed[T any](in []T, zero func(*T)) []T {
	out := slices.Clone(in)
	for i := range out {
		zero(&out[i])
	}
	return out
}

func (w *Watcher) emit(ctx context.Context, name string, events []store.EventRow) {
	now := time.Now()
	for _, e := range events {
		if alert, ok := resolves[e.Kind]; ok {
			_ = w.Store.ResolveEvents(ctx, name, e.Node, alert)
		}
		if e.Kind == "node.removed" {
			for _, k := range []string{"talos.unreachable", "node.notready", "machine.ip-changed"} {
				_ = w.Store.ResolveEvents(ctx, name, e.Node, k)
			}
		}
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
	start := time.Now()
	st, err := w.Manager.Status(ctx, name)
	if err != nil {
		log.Printf("watch %s: %v", name, err)
		return
	}
	now := time.Now()
	w.mu.Lock()
	prev := w.last[name]
	gap := isGap(w.lastTick[name], start, now, w.Interval())
	if st.Observer == cluster.ObserverOnline && (st.APIReachable || anyReachable(st)) {
		w.lastContact[name] = now
	}
	if lc := w.lastContact[name]; !lc.IsZero() {
		st.LastContactAt = lc.UTC().Format(time.RFC3339)
	}
	c := w.confirms[name]
	w.mu.Unlock()
	if gap {
		w.noteGap(now)
	}
	if c == nil {
		fresh := newConfirm()
		if open, err := w.Store.OpenAlerts(ctx, name); err == nil {
			fresh.Seed(open)
		}
		w.mu.Lock()
		if c = w.confirms[name]; c == nil {
			c = fresh
			w.confirms[name] = c
		}
		w.mu.Unlock()
	}

	samples := []store.Sample{{CPUMilli: st.Totals.CPUMilli, CPUCap: st.Totals.CPUCapMilli, MemBytes: st.Totals.MemBytes, MemCap: st.Totals.MemCapBytes, Pods: st.Totals.Pods, Ready: st.Totals.NodesReady == st.Totals.Nodes, Reachable: st.APIReachable}}
	for _, n := range st.Nodes {
		samples = append(samples, store.Sample{Node: n.Hostname, CPUMilli: n.CPUMilli, CPUCap: n.CPUCapMilli, MemBytes: n.MemBytes, MemCap: n.MemCapBytes, Pods: n.Pods, Ready: n.Ready, Reachable: n.TalosReachable})
	}
	_ = w.Store.AddSamples(ctx, name, now, samples)

	offline := st.Observer == cluster.ObserverOffline
	switch {
	case gap:
		w.resetOffline()
	case offline:
		w.noteOffline(st.ObserverError)
	default:
		w.noteOnline()
	}

	switch {
	case st.State != cluster.StateReady:
	case offline:
		c.Reset()
		st.Health, st.OpenAlerts = cluster.HealthUnknown, w.Store.OpenEventCount(ctx, name)
	case gap || (prev != nil && prev.Observer == cluster.ObserverOffline):
		c.Reset()
		health, open := w.health(ctx, name, st, c)
		st.Health, st.OpenAlerts = health, open
	default:
		events := unconfirmed(Derive(name, prev, st))
		if prev == nil {
			events = append(events, w.reconcileOpen(ctx, name, st)...)
		}
		events = append(events, c.Apply(name, st)...)
		w.emit(ctx, name, events)
		health, open := w.health(ctx, name, st, c)
		st.Health, st.OpenAlerts = health, open
	}
	w.mu.Lock()
	if ctx.Err() == nil {
		w.last[name] = st
	}
	w.lastTick[name] = time.Now()
	w.mu.Unlock()
	if !st.APIReachable {
		w.signalKube(name)
	}
	if talosChanged(prev, st) {
		w.refresh(name, k8s.ScopeNodes)
	}
	if w.OnStatus != nil {
		w.OnStatus(name, st)
	}
	w.syncStageWatches(ctx, name, st, gap)
}

type talosFacts struct {
	version, stage, reach string
	reachable             bool
}

func talosFactsOf(n cluster.NodeStatus) talosFacts {
	return talosFacts{version: n.TalosVersion, stage: n.Stage, reach: n.TalosReach, reachable: n.TalosReachable}
}

func talosChanged(prev, cur *cluster.Status) bool {
	if prev == nil || cur == nil {
		return false
	}
	if len(prev.Nodes) != len(cur.Nodes) {
		return true
	}
	before := make(map[string]talosFacts, len(prev.Nodes))
	for _, n := range prev.Nodes {
		before[n.Hostname] = talosFactsOf(n)
	}
	for _, n := range cur.Nodes {
		if f, ok := before[n.Hostname]; !ok || f != talosFactsOf(n) {
			return true
		}
	}
	return false
}

func anyReachable(st *cluster.Status) bool {
	for _, n := range st.Nodes {
		if n.TalosReachable {
			return true
		}
	}
	return false
}

func (w *Watcher) health(ctx context.Context, name string, st *cluster.Status, c *confirm) (string, int) {
	open := w.Store.OpenEventCount(ctx, name)
	for key := range badFacts(name, st) {
		if c.open[key] {
			return cluster.HealthDown, open
		}
	}
	if open > 0 {
		return cluster.HealthDegraded, open
	}
	return cluster.HealthHealthy, open
}

func (w *Watcher) reconcileOpen(ctx context.Context, name string, st *cluster.Status) []store.EventRow {
	var out []store.EventRow
	rec := func(kind, node, msg string) {
		if alert := resolves[kind]; alert != "" && w.Store.HasOpenEvent(ctx, name, node, alert) {
			out = append(out, store.EventRow{Cluster: name, Node: node, Severity: "info", Kind: kind, Message: msg})
		}
	}
	for _, n := range st.Nodes {
		if st.APIReachable && n.MemAllocBytes >= minAllocatableBytes {
			rec("node.memory-ok", n.Hostname, fmt.Sprintf("%s has %d MiB allocatable for pods", n.Hostname, n.MemAllocBytes>>20))
		}
	}
	if st.Platform != nil && st.Platform.Outputs["ingress_ip"] != "" {
		rec("lb.assigned", "", "ingress LoadBalancer IP "+st.Platform.Outputs["ingress_ip"])
	}
	return out
}
