package watch

import (
	"context"
	"sync"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/store"
)

type Watcher struct {
	Manager      *cluster.Manager
	Store        *store.Store
	Alerts       *Alerts
	ScanInterval time.Duration
	OnStatus     func(name string, st *cluster.Status)
	OnRefresh    func(name, scope string)

	OnObserver  func(o ObserverState)
	ScanSubnets func(ctx context.Context)

	interval time.Duration

	mu           sync.Mutex
	last         map[string]*cluster.Status
	confirms     map[string]*confirm
	lastTick     map[string]time.Time
	lastContact  map[string]time.Time
	running      map[string]bool
	observer     ObserverState
	gaps         []time.Time
	offlineTicks int
	samples      *sampleRing

	sigMu       sync.Mutex
	kubeSignals map[string]chan struct{}
}

func New(m *cluster.Manager, interval time.Duration) *Watcher {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	w := &Watcher{Manager: m, Store: m.Store, Alerts: NewAlerts(), ScanInterval: 4 * interval, last: map[string]*cluster.Status{}, confirms: map[string]*confirm{}, lastTick: map[string]time.Time{}, lastContact: map[string]time.Time{}, running: map[string]bool{}, kubeSignals: map[string]chan struct{}{}, samples: newSampleRing(), observer: ObserverState{Online: true}}
	w.interval = interval
	return w
}

func (w *Watcher) Run(ctx context.Context) {
	w.Store.OnChange(func(c store.Change) {
		w.onStoreChange(c)
		if c.Table != "clusters" {
			return
		}
		if row, err := w.Store.GetCluster(c.Cluster); err == nil && cluster.Live(row.State) {
			w.ensureLoop(ctx, c.Cluster)
		}
	})
	for _, r := range w.Store.ListClusters() {
		if cluster.Live(r.State) {
			w.ensureLoop(ctx, r.Name)
		}
	}
	w.candidateLoop(ctx)
}

func (w *Watcher) ensureLoop(ctx context.Context, name string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.running[name] {
		return
	}
	w.running[name] = true
	go w.loop(ctx, name)
}

func (w *Watcher) Latest(name string) *cluster.Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.last[name]
}

func (w *Watcher) Samples(cluster, node string, span time.Duration) []Sample {
	return w.samples.window(cluster, node, span, time.Now())
}
