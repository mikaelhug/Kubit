// Package watch keeps every stored cluster under observation while the daemon runs:
// it polls Status, records capacity samples, and turns state changes into events with
// a severity, so the UI has history and alerts even when nobody was looking.
package watch

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/mikael/kubit/internal/talos"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/k8s"
	"github.com/mikael/kubit/internal/labhost"
	"github.com/mikael/kubit/internal/oob"
	"github.com/mikael/kubit/internal/store"
)

type Watcher struct {
	Manager  *cluster.Manager
	Store    *store.Store
	Interval time.Duration
	// ServiceInterval paces the in-cluster (workload) collection, which lists every
	// pod, service and claim; it is a multiple of Interval.
	ServiceInterval time.Duration
	// OnStatus and OnEvent feed the SSE stream; nil is allowed. OnRefresh tells the
	// UI a view of a cluster changed (Kubernetes informers, debounced).
	OnStatus  func(name string, st *cluster.Status)
	OnEvent   func(e store.EventRow)
	OnRefresh func(name, scope string)
	// OnHostSample pushes a lab host's newest reading (keyed by MAC).
	OnHostSample func(mac string, sm store.Sample)

	// OnObserver reports when Kubit's own view of the network changes.
	OnObserver func(o ObserverState)

	mu           sync.Mutex
	last         map[string]*cluster.Status
	lastServices map[string]*cluster.ServiceHealth
	trackers     map[string]*ServiceTracker
	confirms     map[string]*confirm
	lastTick     map[string]time.Time
	lastContact  map[string]time.Time
	running      map[string]context.CancelFunc
	memHigh      map[string]int // lab host MAC → consecutive samples over the memory line
	observer     ObserverState
	gaps         []time.Time
	labNoNet     map[string]bool
	offlineTicks int
}

// offlineAfter is how many consecutive blind observations declare the observer
// offline: a DarkWake runs one or two ticks without a network and must stay silent.
const offlineAfter = 3

// isGap tells whether observation paused between two ticks: the new tick started long
// after the previous one ended, or the tick itself spanned a suspension.
func isGap(lastEnd, start, end time.Time, interval time.Duration) bool {
	if lastEnd.IsZero() {
		return false
	}
	return start.Sub(lastEnd) > 2*interval || end.Sub(start) > 2*interval
}

// noteOffline counts a blind observation and flips the observer state once enough
// have been seen in a row; noteOnline resets both.
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

// ObserverState is what Kubit knows about its own ability to observe: whether its
// host can reach the network, since when, and how often observation paused (the
// host slept or the process was suspended) in the last day.
type ObserverState struct {
	Online    bool   `json:"online"`
	Since     string `json:"since,omitempty"`
	Error     string `json:"error,omitempty"`
	Gaps24h   int    `json:"gaps24h"`
	LastGapAt string `json:"lastGapAt,omitempty"`
}

// Observer returns the current observer state.
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

// setOnline records a change of the observer's network view and reports it once.
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
	return &Watcher{Manager: m, Store: m.Store, Interval: interval, ServiceInterval: 4 * interval, last: map[string]*cluster.Status{}, lastServices: map[string]*cluster.ServiceHealth{}, trackers: map[string]*ServiceTracker{}, confirms: map[string]*confirm{}, lastTick: map[string]time.Time{}, lastContact: map[string]time.Time{}, running: map[string]context.CancelFunc{}, memHigh: map[string]int{}, labNoNet: map[string]bool{}, observer: ObserverState{Online: true}}
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
	go w.labLoop(ctx)
	go w.candidateLoop(ctx)
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
	go w.watchKubernetes(ctx, name)
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

// watchKubernetes keeps informers open on the cluster and reports changed scopes;
// it reconnects with backoff while the API is unreachable or the cluster is not
// ready yet. Change bursts (a rollout touches dozens of objects) collapse to one
// refresh per scope per second.
func (w *Watcher) watchKubernetes(ctx context.Context, name string) {
	deb := k8s.NewDebouncer(time.Second, func(scope string) {
		if w.OnRefresh != nil {
			w.OnRefresh(name, scope)
		}
	})
	backoff := 5 * time.Second
	for ctx.Err() == nil {
		st := w.Latest(name)
		if st == nil || !st.APIReachable || (st.State != cluster.StateReady && st.State != cluster.StateBootstrapped) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			continue
		}
		kc, err := w.Manager.KubeClient(ctx, name)
		if err != nil {
			log.Printf("watch %s: informers: %v", name, err)
		} else {
			wctx, cancel := context.WithCancel(ctx)
			started := time.Now()
			kc.WatchScopes(wctx, deb.Hit)
			cancel()
			if time.Since(started) > time.Minute {
				backoff = 5 * time.Second
			}
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

// candidateLoop keeps the wizard's picker honest: every unassigned machine is probed
// each service interval, so last-seen moves while it answers and stops when it is
// gone. Lab VMs are covered by their host's tick.
func (w *Watcher) candidateLoop(ctx context.Context) {
	t := time.NewTicker(w.ServiceInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		rows, err := w.Store.ListNodes(ctx, "")
		if err != nil {
			continue
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
					_ = w.Store.UpsertNode(ctx, rowFromScan(res))
				}
			case store.KindUnbooted:
				switch {
				case m.OOB != nil && m.OOB.Type == "redfish":
					pctx, cancel := context.WithTimeout(ctx, 6*time.Second)
					_, ok := oob.ProbeRedfish(pctx, m.OOB.Host, 2*time.Second)
					cancel()
					if ok {
						_ = w.Store.UpsertNode(ctx, store.NodeRow{MAC: m.MAC, IP: m.IP, Source: "redfish", State: m.State})
					}
				case m.OOB != nil && portOpen(m.OOB.Host, "16992"), portOpen(m.IP, "16992"):
					_ = w.Store.UpsertNode(ctx, store.NodeRow{MAC: m.MAC, IP: m.IP, Source: "amt", State: m.State})
				}
			}
		}
	}
}

func portOpen(host, port string) bool {
	if host == "" {
		return false
	}
	c, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), 2*time.Second)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// labLoop keeps lab hosts current: VM list and capacity over SSH, and VMs that were
// started into Talos maintenance mode get their row flipped as soon as they answer.
func (w *Watcher) labLoop(ctx context.Context) {
	t := time.NewTicker(w.ServiceInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		rows, err := w.Store.ListNodes(ctx, "")
		if err != nil {
			continue
		}
		for i := range rows {
			host := rows[i]
			if host.LabHost == nil || host.LabHost.State != "ready" {
				continue
			}
			w.labTick(ctx, &host)
		}
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
	// The package index is refreshed hourly; it needs the network and a minute.
	if u := host.LabHost.Updates; u == nil || staleBy(u.CheckedAt, time.Hour) {
		if u, err := lc.CheckUpdates(tctx); err == nil {
			host.LabHost.Updates = &u
			if (u.Count > 0 || u.NeedsReboot()) && time.Since(w.Store.LastEventAt(tctx, key, "labhost.updates")) > 24*time.Hour {
				w.emit(tctx, key, []store.EventRow{{Cluster: key, Severity: "info", Kind: "labhost.updates", Message: labName(host) + ": " + updatesSummary(u)}})
			}
		}
	}
	// Merge only the fields this tick computed; never touch State/Error, which an
	// operation (e.g. an in-flight maintenance run) may have changed to "updating".
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
				r := rowFromScan(res)
				r.Source = "lab"
				_ = w.Store.UpsertNode(tctx, r)
				_ = w.Store.SetMachineHost(tctx, vm.MAC, host.MAC)
			}
		}
	}
}

// labUnreachableAfter is how many consecutive failed ticks make an alert: one is a
// blip, three minutes without SSH is a host that is down or cut off.
const labUnreachableAfter = 3

func (w *Watcher) labFailed(ctx context.Context, host *store.Machine, err error) {
	key := store.LabHostKey(host.MAC)
	if cluster.Classify(err) == cluster.ReachNoNetwork {
		if r, _ := cluster.ControlProbe(2 * time.Second); r == cluster.ReachNoNetwork {
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
	// Bump only Failures against the current record; leave State/VMs/etc. alone so a
	// failing tick during a maintenance reboot does not un-park the host.
	var failures int
	_ = w.Store.UpdateLabHost(ctx, host.MAC, func(lh *store.LabHost) {
		lh.Failures++
		failures = lh.Failures
	})
	host.LabHost.Failures = failures
	if failures == labUnreachableAfter {
		w.emit(ctx, key, []store.EventRow{{Cluster: key, Severity: "critical", Kind: "labhost.unreachable", Message: fmt.Sprintf("%s: no SSH for %d checks (%v)", labName(host), labUnreachableAfter, err)}})
	}
	_ = w.Store.AddSamples(ctx, key, time.Now(), []store.Sample{{Reachable: false}})
}

// Thresholds on the filesystem carrying thin-provisioned VM disks: at 100 % every VM
// pauses at once, so the warning comes early and clears with hysteresis.
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

func rowFromScan(res talos.ScanResult) store.NodeRow {
	row := store.NodeRow{IP: res.IP, Source: "scan", State: string(res.State)}
	if inv := res.Inventory; inv != nil {
		row.MAC, row.Arch, row.TalosVersion = inv.PrimaryMAC(), inv.Arch, inv.TalosVersion
		row.UUID, row.Serial = inv.UUID, inv.Serial
		row.Hardware, _ = json.Marshal(inv)
	}
	return row
}

// disruptive operations restart pods by design; workload rules stay quiet for a while
// after one so the churn is not reported as crashloops.
var disruptive = []string{"cluster.create", "cluster.apply", "etcd.restore", "upgrade.talos", "upgrade.kubernetes", "node.add", "node.remove", "node.reboot", "node.rename", "node.pool", "node.readdress", "node.upgrade", "platform.apply", "labhost.update", "labhost.reboot"}

const quietAfterOperation = 10 * time.Minute

// serviceTick collects what runs in the cluster and raises/resolves workload alerts.
// It is skipped while the API server is unreachable (Status already alerts on that),
// while the cluster is provisioning, and during the quiet window after an operation.
func (w *Watcher) serviceTick(ctx context.Context, name string) {
	if st := w.Latest(name); st == nil || !st.APIReachable || st.State != cluster.StateReady {
		return
	}
	if last := w.Store.LastFinished(ctx, name, disruptive); time.Since(last) < quietAfterOperation {
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
		if e.Kind == "node.removed" {
			for _, k := range []string{"talos.unreachable", "node.notready", "machine.ip-changed"} {
				_ = w.Store.ResolveEvents(ctx, name, e.Node, k)
			}
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
	start := time.Now()
	st, err := w.Manager.Status(ctx, name)
	if err != nil {
		log.Printf("watch %s: %v", name, err)
		return
	}
	now := time.Now()
	w.mu.Lock()
	prev := w.last[name]
	w.last[name] = st
	// A tick long after the previous one, or one that took far longer than it should,
	// means the laptop slept: what was observed before is no baseline for now.
	gap := isGap(w.lastTick[name], start, now, w.Interval)
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
	if c == nil {
		c = newConfirm()
		if open, err := w.Store.Events(ctx, name, 1000, true); err == nil {
			c.Seed(open)
		}
		w.confirms[name] = c
	}
	w.mu.Unlock()

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

	// While a cluster is still being provisioned, unreachable nodes and a missing API
	// are the expected state, not alerts; samples and status still flow to the UI.
	switch {
	case st.State != cluster.StateReady:
	case offline:
		// Nothing the cluster did: the observer is blind. Counters restart when it sees again.
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

// health rolls the confirmed facts and the open alerts into one word for the cluster
// pill: down only for facts that have held long enough to be alerts.
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

// reconcileOpen closes alerts left open from before a daemon restart whose condition
// no longer holds, for the kinds the confirm tracker does not own: Derive only reports
// transitions, so without this a transient failure observed right before a restart
// would stay "active" for ever.
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

// resolves maps a recovery event to the alert kind it clears.
var resolves = map[string]string{
	"talos.back": "talos.unreachable", "node.ready": "node.notready", "node.memory-ok": "node.memory-small", "api.back": "api.unreachable", "etcd.healthy": "etcd.unhealthy", "lb.assigned": "lb.lost",
	"labhost.back": "labhost.unreachable", "labhost.disk-ok": "labhost.disk-low", "labhost.memory-ok": "labhost.memory-pressure",
}

// minAllocatableBytes is the allocatable memory under which a node cannot carry the
// platform add-ons; a 1 GiB VM leaves about 450 MiB after Talos and the kubelet.
const minAllocatableBytes = 768 << 20

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
