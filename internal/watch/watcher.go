package watch

import (
	"context"
	"sync"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
)

type Watcher struct {
	Manager         *cluster.Manager
	Store           *store.Store
	ServiceInterval time.Duration
	OnStatus        func(name string, st *cluster.Status)
	OnEvent         func(e store.EventRow)
	OnRefresh       func(name, scope string)

	OnObserver func(o ObserverState)

	interval time.Duration

	mu           sync.Mutex
	stopping     map[string]chan struct{}
	last         map[string]*cluster.Status
	lastServices map[string]*cluster.ServiceHealth
	trackers     map[string]*ServiceTracker
	confirms     map[string]*confirm
	lastTick     map[string]time.Time
	lastContact  map[string]time.Time
	running      map[string]*clusterLoop
	observer     ObserverState
	gaps         []time.Time
	offlineTicks int
	stages       map[string]map[string]*stageWatch
	watchStage   stageSource

	sigMu       sync.Mutex
	kubeSignals map[string]chan struct{}
}

func New(m *cluster.Manager, interval time.Duration) *Watcher {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	w := &Watcher{Manager: m, Store: m.Store, ServiceInterval: 4 * interval, stopping: map[string]chan struct{}{}, last: map[string]*cluster.Status{}, lastServices: map[string]*cluster.ServiceHealth{}, trackers: map[string]*ServiceTracker{}, confirms: map[string]*confirm{}, lastTick: map[string]time.Time{}, lastContact: map[string]time.Time{}, running: map[string]*clusterLoop{}, kubeSignals: map[string]chan struct{}{}, stages: map[string]map[string]*stageWatch{}, watchStage: talos.WatchStage, observer: ObserverState{Online: true}}
	w.interval = interval
	return w
}

func (w *Watcher) Interval() time.Duration { return w.interval }

type clusterLoop struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (w *Watcher) Run(ctx context.Context) {
	w.Store.OnChange(w.onStoreChange)
	reconcile := func() {
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
	reconcile()
	go w.candidateLoop(ctx)
	t := time.NewTicker(w.Interval())
	prune := time.NewTicker(time.Hour)
	defer t.Stop()
	defer prune.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			reconcile()
		case <-prune.C:
			_ = w.Store.Prune(ctx)
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
			<-prev
		}
		if cctx.Err() == nil {
			w.loop(cctx, name)
		}
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
	w.dropStageWatches(name)
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
