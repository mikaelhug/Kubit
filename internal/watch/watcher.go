package watch

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mikael/kubit/internal/talos"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/k8s"
	"github.com/mikael/kubit/internal/labhost"
	"github.com/mikael/kubit/internal/oob"
	"github.com/mikael/kubit/internal/store"
)

type Watcher struct {
	Manager         *cluster.Manager
	Store           *store.Store
	ServiceInterval time.Duration
	OnStatus        func(name string, st *cluster.Status)
	OnEvent         func(e store.EventRow)
	OnRefresh       func(name, scope string)
	OnHostSample    func(mac string, sm store.Sample)

	OnObserver func(o ObserverState)

	interval atomic.Int64

	mu           sync.Mutex
	retune       chan struct{}
	stopping     map[string]chan struct{}
	last         map[string]*cluster.Status
	lastServices map[string]*cluster.ServiceHealth
	trackers     map[string]*ServiceTracker
	confirms     map[string]*confirm
	lastTick     map[string]time.Time
	lastContact  map[string]time.Time
	running      map[string]*clusterLoop
	memHigh      map[string]int
	observer     ObserverState
	gaps         []time.Time
	labNoNet     map[string]bool
	offlineTicks int

	sigMu       sync.Mutex
	kubeSignals map[string]chan struct{}
}

const offlineAfter = 3

func isGap(lastEnd, start, end time.Time, interval time.Duration) bool {
	if lastEnd.IsZero() {
		return false
	}
	return start.Sub(lastEnd) > 2*interval || end.Sub(start) > 2*interval
}

func (w *Watcher) noteOffline(reason string) {
	w.mu.Lock()
	w.offlineTicks++
	flip := w.offlineTicks >= offlineAfter
	w.mu.Unlock()
	if flip {
		w.setOnline(false, reason)
	}
}

func (w *Watcher) noteOnline() {
	w.mu.Lock()
	w.offlineTicks = 0
	w.mu.Unlock()
	w.setOnline(true, "")
}

func (w *Watcher) resetOffline() {
	w.mu.Lock()
	w.offlineTicks = 0
	w.mu.Unlock()
}

type ObserverState struct {
	Online    bool   `json:"online"`
	Since     string `json:"since,omitempty"`
	Error     string `json:"error,omitempty"`
	Gaps24h   int    `json:"gaps24h"`
	LastGapAt string `json:"lastGapAt,omitempty"`
}

func (w *Watcher) Observer() ObserverState {
	w.mu.Lock()
	defer w.mu.Unlock()
	o := w.observer
	o.Gaps24h, o.LastGapAt = w.gapStats()
	return o
}

func (w *Watcher) gapStats() (int, string) {
	cut := time.Now().Add(-24 * time.Hour)
	kept := w.gaps[:0]
	for _, g := range w.gaps {
		if g.After(cut) {
			kept = append(kept, g)
		}
	}
	w.gaps = kept
	if len(kept) == 0 {
		return 0, ""
	}
	return len(kept), kept[len(kept)-1].UTC().Format(time.RFC3339)
}

func (w *Watcher) setOnline(online bool, reason string) {
	w.mu.Lock()
	changed := w.observer.Online != online || w.observer.Since == ""
	if changed {
		w.observer = ObserverState{Online: online, Since: time.Now().UTC().Format(time.RFC3339), Error: reason}
	}
	o := w.observer
	o.Gaps24h, o.LastGapAt = w.gapStats()
	w.mu.Unlock()
	if changed && w.OnObserver != nil {
		w.OnObserver(o)
	}
}

func New(m *cluster.Manager, interval time.Duration) *Watcher {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	w := &Watcher{Manager: m, Store: m.Store, ServiceInterval: 4 * interval, retune: make(chan struct{}), stopping: map[string]chan struct{}{}, last: map[string]*cluster.Status{}, lastServices: map[string]*cluster.ServiceHealth{}, trackers: map[string]*ServiceTracker{}, confirms: map[string]*confirm{}, lastTick: map[string]time.Time{}, lastContact: map[string]time.Time{}, running: map[string]*clusterLoop{}, memHigh: map[string]int{}, labNoNet: map[string]bool{}, kubeSignals: map[string]chan struct{}{}, observer: ObserverState{Online: true}}
	w.interval.Store(int64(interval))
	return w
}

func (w *Watcher) Interval() time.Duration { return time.Duration(w.interval.Load()) }

func (w *Watcher) SetInterval(d time.Duration) {
	if d <= 0 || time.Duration(w.interval.Swap(int64(d))) == d {
		return
	}
	w.mu.Lock()
	close(w.retune)
	w.retune = make(chan struct{})
	w.mu.Unlock()
}

func (w *Watcher) retuned() <-chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.retune
}

type clusterLoop struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (w *Watcher) Run(ctx context.Context) {
	w.Store.OnChange(w.onStoreChange)
	sync := func() {
		rows, err := w.Store.ListClusters(ctx)
		if err != nil {
			return
		}
		want := map[string]bool{}
		for _, r := range rows {
			if cluster.Observable(r.State) {
				want[r.Name] = true
			}
		}
		w.mu.Lock()
		defer w.mu.Unlock()
		for name := range want {
			if _, ok := w.running[name]; !ok {
				w.startLoop(ctx, name)
			}
		}
		for name, l := range w.running {
			if !want[name] {
				l.cancel()
				delete(w.running, name)
				w.stopping[name] = l.done
			}
		}
	}
	sync()
	go w.labLoop(ctx)
	go w.candidateLoop(ctx)
	t := time.NewTicker(w.Interval())
	prune := time.NewTicker(time.Hour)
	defer t.Stop()
	defer prune.Stop()
	retune := w.retuned()
	for {
		select {
		case <-ctx.Done():
			return
		case <-retune:
			retune = w.retuned()
			t.Reset(w.Interval())
		case <-t.C:
			sync()
		case <-prune.C:
			_ = w.Store.PruneSamples(ctx)
		}
	}
}

func (w *Watcher) startLoop(ctx context.Context, name string) {
	prev := w.stopping[name]
	delete(w.stopping, name)
	cctx, cancel := context.WithCancel(ctx)
	l := &clusterLoop{cancel: cancel, done: make(chan struct{})}
	w.running[name] = l
	go func() {
		defer close(l.done)
		if prev != nil {
			select {
			case <-prev:
			case <-cctx.Done():
				return
			}
		}
		w.loop(cctx, name)
		w.forget(name, l.done)
	}()
}

func (w *Watcher) forget(name string, done chan struct{}) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopping[name] == done {
		delete(w.stopping, name)
	}
	delete(w.last, name)
	delete(w.trackers, name)
	delete(w.lastServices, name)
	delete(w.confirms, name)
	delete(w.lastTick, name)
	delete(w.lastContact, name)
}

func (w *Watcher) Latest(name string) *cluster.Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.last[name]
}

func (w *Watcher) LatestServices(name string) *cluster.ServiceHealth {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastServices[name]
}

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
		st := w.Latest(name)
		if st == nil || !st.APIReachable || !cluster.Observable(st.State) {
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

func kubeScopes(scope, namespace string) []string {
	if scope == k8s.ScopeWorkloads {
		if key, ok := cluster.PlatformNamespace(namespace); ok && key != "kubernetes" {
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
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}
}

func (w *Watcher) candidateLoop(ctx context.Context) {
	w.every(ctx, w.ServiceInterval, w.candidateTick)
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

func (w *Watcher) candidateTick(ctx context.Context) {
	rows, err := w.Store.ListNodes(ctx, "")
	if err != nil {
		return
	}
	for _, m := range rows {
		if m.IsLabVM() || m.IP == "" {
			continue
		}
		switch m.Kind() {
		case store.KindMaintenance, store.KindConfigured:
			pctx, cancel := context.WithTimeout(ctx, 6*time.Second)
			res := talos.Probe(pctx, m.IP, 2*time.Second)
			cancel()
			if res.Err == nil {
				_ = w.Store.UpsertNode(ctx, cluster.RowFromScan(res))
			}
		case store.KindUnbooted:
			switch {
			case m.OOB != nil && m.OOB.Type == "redfish":
				pctx, cancel := context.WithTimeout(ctx, 6*time.Second)
				ok := oob.ProbeRedfish(pctx, m.OOB.Host, 2*time.Second)
				cancel()
				if ok {
					_ = w.Store.UpsertNode(ctx, store.NodeRow{MAC: m.MAC, IP: m.IP, Source: "redfish", State: m.State})
				}
			case m.OOB != nil && portOpen(ctx, m.OOB.Host, oob.AMTPort), portOpen(ctx, m.IP, oob.AMTPort):
				_ = w.Store.UpsertNode(ctx, store.NodeRow{MAC: m.MAC, IP: m.IP, Source: "amt", State: m.State})
			}
		}
	}
}

func portOpen(ctx context.Context, host, port string) bool {
	return host != "" && talos.TCPErr(ctx, host, port, 2*time.Second) == nil
}

func (w *Watcher) labLoop(ctx context.Context) {
	w.every(ctx, w.ServiceInterval, w.labHostsTick)
}

func (w *Watcher) labHostsTick(ctx context.Context) {
	rows, err := w.Store.ListNodes(ctx, "")
	if err != nil {
		return
	}
	for i := range rows {
		host := rows[i]
		if host.LabHost == nil || host.LabHost.State != "ready" {
			continue
		}
		w.labTick(ctx, &host)
	}
}

func (w *Watcher) labTick(ctx context.Context, host *store.Machine) {
	tctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	key := store.LabHostKey(host.MAC)
	lc, err := w.Manager.LabDial(tctx, host)
	if err != nil {
		w.labFailed(tctx, host, err)
		return
	}
	defer lc.Close()
	vms, err := lc.List(tctx)
	if err != nil {
		w.labFailed(tctx, host, err)
		return
	}
	if host.LabHost.Failures >= labUnreachableAfter {
		w.emit(tctx, key, []store.EventRow{{Cluster: key, Severity: "info", Kind: "labhost.back", Message: labName(host) + ": reachable again"}})
	}
	w.mu.Lock()
	w.labNoNet[host.MAC] = false
	w.mu.Unlock()
	w.noteOnline()
	host.LabHost.Failures = 0
	capa, err := lc.Capacity(tctx)
	if err == nil {
		host.LabHost.Capacity = capa
	}
	host.LabHost.VMs = vms
	now := time.Now()
	if m, err := lc.Metrics(tctx); err == nil {
		host.LabHost.Metrics = &m
		sm := store.Sample{CPUMilli: int64(m.CPUPct * 10), CPUCap: 1000, MemBytes: m.MemUsed, MemCap: m.MemTotal, Pods: m.VMsRunning, Ready: true, Reachable: true, Disk: m.DiskUsed, DiskCap: m.DiskTotal}
		_ = w.Store.AddSamples(tctx, key, now, []store.Sample{sm})
		if w.OnHostSample != nil {
			sm.TS = now.UTC().Format(time.RFC3339)
			w.OnHostSample(host.MAC, sm)
		}
		w.emit(tctx, key, w.labResourceEvents(tctx, host, m))
	}
	if up, ok := lc.(labhost.Updater); ok && (host.LabHost.Updates == nil || staleBy(host.LabHost.Updates.CheckedAt, time.Hour)) {
		if u, err := up.CheckUpdates(tctx); err == nil {
			host.LabHost.Updates = &u
			if (u.Count > 0 || u.NeedsReboot()) && time.Since(w.Store.LastEventAt(tctx, key, "labhost.updates")) > 24*time.Hour {
				w.emit(tctx, key, []store.EventRow{{Cluster: key, Severity: "info", Kind: "labhost.updates", Message: labName(host) + ": " + updatesSummary(u)}})
			}
		}
	}
	_ = w.Store.UpdateLabHost(tctx, host.MAC, func(lh *store.LabHost) {
		lh.Failures = host.LabHost.Failures
		lh.Capacity = host.LabHost.Capacity
		lh.VMs = host.LabHost.VMs
		lh.Metrics = host.LabHost.Metrics
		lh.Updates = host.LabHost.Updates
	})
	for _, vm := range vms {
		row, err := w.Store.GetMachine(tctx, vm.MAC)
		if err != nil {
			continue
		}
		if vm.State != "running" && row.Cluster == "" && row.State != "off" {
			_ = w.Store.SetNodeState(tctx, row.IP, "off")
			continue
		}
		if vm.State == "running" && row.State == "off" {
			_ = w.Store.SetNodeState(tctx, row.IP, "booting")
			row.State = "booting"
		}
		if vm.State == "running" && vm.IP != "" && (row.State == "booting" || row.IP == "") {
			res := talos.Probe(tctx, vm.IP, 2*time.Second)
			if res.Err == nil {
				r := cluster.RowFromScan(res)
				r.Source = "lab"
				_ = w.Store.UpsertNode(tctx, r)
				_ = w.Store.SetMachineHost(tctx, vm.MAC, host.MAC)
			}
		}
	}
}

const labUnreachableAfter = 3

func (w *Watcher) labFailed(ctx context.Context, host *store.Machine, err error) {
	key := store.LabHostKey(host.MAC)
	if cluster.Classify(err) == cluster.ReachNoNetwork {
		if cluster.ControlProbe(ctx, 2*time.Second) == cluster.ReachNoNetwork {
			w.mu.Lock()
			first := !w.labNoNet[host.MAC]
			w.labNoNet[host.MAC] = true
			w.mu.Unlock()
			if first {
				log.Printf("lab host %s: %v (Kubit's host cannot reach the network; not counted)", host.MAC, err)
			}
			w.noteOffline(cluster.ShortNet(err))
			return
		}
	}
	w.mu.Lock()
	w.labNoNet[host.MAC] = false
	w.mu.Unlock()
	log.Printf("lab host %s: %v", host.MAC, err)
	var failures int
	_ = w.Store.UpdateLabHost(ctx, host.MAC, func(lh *store.LabHost) {
		lh.Failures++
		failures = lh.Failures
	})
	host.LabHost.Failures = failures
	if failures == labUnreachableAfter {
		what := "no SSH"
		if host.LabHost.Driver != "" {
			what = "not answering"
		}
		w.emit(ctx, key, []store.EventRow{{Cluster: key, Severity: "critical", Kind: "labhost.unreachable", Message: fmt.Sprintf("%s: %s for %d checks (%v)", labName(host), what, labUnreachableAfter, err)}})
	}
	_ = w.Store.AddSamples(ctx, key, time.Now(), []store.Sample{{Reachable: false}})
}

const (
	labDiskWarn     = 85
	labDiskCritical = 95
	labDiskOK       = 80
	labMemWarn      = 92
	labMemOK        = 85
	labMemSamples   = 3
)

func (w *Watcher) labResourceEvents(ctx context.Context, host *store.Machine, m labhost.Metrics) []store.EventRow {
	key, name := store.LabHostKey(host.MAC), labName(host)
	var out []store.EventRow
	if m.DiskTotal > 0 {
		pct := int(m.DiskUsed * 100 / m.DiskTotal)
		open := w.Store.OpenEventSeverity(ctx, key, "", "labhost.disk-low")
		msg := fmt.Sprintf("%s: VM disk %d%% full (%s of %s)", name, pct, humanGiB(m.DiskUsed), humanGiB(m.DiskTotal))
		switch {
		case pct >= labDiskCritical && open != "critical":
			_ = w.Store.ResolveEvents(ctx, key, "", "labhost.disk-low")
			out = append(out, store.EventRow{Cluster: key, Severity: "critical", Kind: "labhost.disk-low", Message: msg})
		case pct >= labDiskWarn && pct < labDiskCritical && open == "":
			out = append(out, store.EventRow{Cluster: key, Severity: "warn", Kind: "labhost.disk-low", Message: msg})
		case pct < labDiskOK && open != "":
			out = append(out, store.EventRow{Cluster: key, Severity: "info", Kind: "labhost.disk-ok", Message: fmt.Sprintf("%s: VM disk back to %d%%", name, pct)})
		}
	}
	if m.MemTotal > 0 {
		pct := int(m.MemUsed * 100 / m.MemTotal)
		w.mu.Lock()
		if pct >= labMemWarn {
			w.memHigh[host.MAC]++
		} else {
			w.memHigh[host.MAC] = 0
		}
		high := w.memHigh[host.MAC]
		w.mu.Unlock()
		open := w.Store.HasOpenEvent(ctx, key, "", "labhost.memory-pressure")
		switch {
		case high >= labMemSamples && !open:
			out = append(out, store.EventRow{Cluster: key, Severity: "warn", Kind: "labhost.memory-pressure", Message: fmt.Sprintf("%s: memory %d%% used for %d checks; VMs may be swapped or killed", name, pct, high)})
		case pct < labMemOK && open:
			out = append(out, store.EventRow{Cluster: key, Severity: "info", Kind: "labhost.memory-ok", Message: fmt.Sprintf("%s: memory back to %d%%", name, pct)})
		}
	}
	return out
}

func labName(host *store.Machine) string {
	if host.LabHost != nil && host.LabHost.Capacity.Hostname != "" {
		return host.LabHost.Capacity.Hostname
	}
	if host.Hostname != "" {
		return host.Hostname
	}
	return host.MAC
}

func updatesSummary(u labhost.Updates) string {
	var parts []string
	if u.Count > 0 {
		parts = append(parts, fmt.Sprintf("%d package updates pending", u.Count))
	}
	if u.NeedsReboot() {
		parts = append(parts, "reboot required")
	}
	return strings.Join(parts, ", ")
}

func humanGiB(b int64) string { return fmt.Sprintf("%.0f GiB", float64(b)/(1<<30)) }

func staleBy(ts string, d time.Duration) bool {
	t, err := time.Parse(time.RFC3339, ts)
	return err != nil || time.Since(t) > d
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
	w.lastServices[name] = sh
	w.mu.Unlock()
	w.refresh(name, k8s.ScopeServices)
	if last := w.Store.LastFinished(ctx, name, disruptive); time.Since(last) < quietAfterOperation {
		return
	}
	w.mu.Lock()
	tr := w.trackers[name]
	w.mu.Unlock()
	if tr == nil {
		fresh := newServiceTracker()
		if open, err := w.Store.Events(ctx, name, 1000, true); err == nil {
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

func (w *Watcher) emit(ctx context.Context, name string, events []store.EventRow) {
	now := time.Now()
	for _, e := range events {
		if resolves, ok := resolves[e.Kind]; ok {
			_ = w.Store.ResolveEvents(ctx, name, e.Node, resolves)
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
	if gap {
		w.gaps = append(w.gaps, now)
	}
	if st.Observer == cluster.ObserverOnline && (st.APIReachable || anyReachable(st)) {
		w.lastContact[name] = now
	}
	if lc := w.lastContact[name]; !lc.IsZero() {
		st.LastContactAt = lc.UTC().Format(time.RFC3339)
	}
	c := w.confirms[name]
	w.mu.Unlock()
	if c == nil {
		fresh := newConfirm()
		if open, err := w.Store.Events(ctx, name, 1000, true); err == nil {
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
	if w.OnStatus != nil {
		w.OnStatus(name, st)
	}
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

var resolves = map[string]string{
	"talos.back": "talos.unreachable", "node.ready": "node.notready", "node.memory-ok": "node.memory-small", "api.back": "api.unreachable", "etcd.healthy": "etcd.unhealthy", "lb.assigned": "lb.lost",
	"labhost.back": "labhost.unreachable", "labhost.disk-ok": "labhost.disk-low", "labhost.memory-ok": "labhost.memory-pressure",
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
