package watch

import (
	"bytes"
	"context"
	"log"
	"time"

	"github.com/mikaelhug/kubit/internal/cluster"
	"github.com/mikaelhug/kubit/internal/k8s"
	"github.com/mikaelhug/kubit/internal/store"
)

func (w *Watcher) loop(ctx context.Context, name string) {
	kube := make(chan struct{})
	go func() {
		defer close(kube)
		w.watchKubernetes(ctx, name)
	}()
	defer func() { <-kube }()
	nudge := make(chan struct{}, 1)
	w.mu.Lock()
	w.nudges[name] = nudge
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		delete(w.nudges, name)
		w.mu.Unlock()
	}()
	w.every(ctx, w.interval, nudge, func(ctx context.Context) { w.tick(ctx, name) })
}

func (w *Watcher) CheckNow(name string) bool {
	w.mu.Lock()
	nudge := w.nudges[name]
	w.mu.Unlock()
	if nudge == nil {
		return false
	}
	select {
	case nudge <- struct{}{}:
	default:
	}
	return true
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
	sec, err := w.Store.GetClusterSecrets(name)
	if err != nil {
		return nil, nil, err
	}
	kc, err := w.Manager.KubeClientFor(name, sec.Kubeconfig)
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
			sec, err := w.Store.GetClusterSecrets(name)
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
	return st != nil && st.APIReachable
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
	if c.Table == "secrets" {
		w.signalKube(c.Cluster)
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

func (w *Watcher) every(ctx context.Context, d time.Duration, nudge <-chan struct{}, tick func(context.Context)) {
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-nudge:
			t.Reset(d)
		}
	}
}

const quietAfterOperation = 10 * time.Minute

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
	gap := isGap(w.lastTick[name], start, now, w.interval)
	if st.Observer == cluster.ObserverOnline && (st.APIReachable || anyReachable(st)) {
		w.lastContact[name] = now
	}
	if lc := w.lastContact[name]; !lc.IsZero() {
		st.LastContactAt = lc.UTC().Format(time.RFC3339)
	}
	c := w.confirms[name]
	if c == nil {
		c = newConfirm()
		if open := w.Alerts.Open(name); len(open) > 0 {
			c.Seed(open)
		}
		w.confirms[name] = c
	}
	w.mu.Unlock()
	if gap {
		w.noteGap(now)
	}

	samples := []Sample{{CPUMilli: st.Totals.CPUMilli, CPUCap: st.Totals.CPUCapMilli, MemBytes: st.Totals.MemBytes, MemCap: st.Totals.MemCapBytes, Pods: st.Totals.Pods, Ready: st.Totals.NodesReady == st.Totals.Nodes, Reachable: st.APIReachable}}
	for _, n := range st.Nodes {
		samples = append(samples, Sample{Node: n.Hostname, CPUMilli: n.CPUMilli, CPUCap: n.CPUCapMilli, MemBytes: n.MemBytes, MemCap: n.MemCapBytes, Pods: n.Pods, Ready: n.Ready, Reachable: n.TalosReachable})
	}
	w.samples.add(name, now, samples)

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
		st.Health, st.OpenAlerts = cluster.HealthUnknown, w.Alerts.OpenCount(name)
	case gap || (prev != nil && prev.Observer == cluster.ObserverOffline):
		c.Reset()
		st.Health, st.OpenAlerts = w.health(name, st, c)
	default:
		for _, e := range Derive(name, prev, st) {
			w.Alerts.Record(e)
		}
		facts := badFacts(name, st)
		if len(facts) > 0 && w.Manager.ApplyQuiet(ctx, name, quietAfterOperation) {
			c.Reset()
		} else {
			w.apply(c.Apply(name, facts))
		}
		st.Health, st.OpenAlerts = w.health(name, st, c)
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
}

func (w *Watcher) apply(transitions []transition) {
	for _, t := range transitions {
		if t.raise != nil {
			w.Alerts.Raise(*t.raise)
			continue
		}
		w.Alerts.Resolve(t.recovery.Cluster, t.recovery.Node, t.resolves, t.recovery)
	}
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

func (w *Watcher) health(name string, st *cluster.Status, c *confirm) (string, int) {
	open := w.Alerts.OpenCount(name)
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
