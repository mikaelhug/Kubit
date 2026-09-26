package watch

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mikael/kubit/internal/cluster"
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
