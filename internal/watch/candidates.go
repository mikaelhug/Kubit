package watch

import (
	"context"
	"sync"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
)

const (
	pingEvery     = 10 * time.Second
	missesOffline = 2
)

func (w *Watcher) candidateLoop(ctx context.Context) {
	go w.pingLoop(ctx)
	w.every(ctx, w.ScanInterval, nil, w.candidateTick)
}

func (w *Watcher) candidateTick(ctx context.Context) {
	if w.ScanSubnets != nil {
		w.ScanSubnets(ctx)
	}
	for _, m := range w.Store.ListNodes("") {
		if k := m.Kind(); m.IP != "" && (k == store.KindMaintenance || k == store.KindConfigured) {
			w.reprobe(ctx, m)
		}
	}
}

func (w *Watcher) reprobe(ctx context.Context, m store.Machine) {
	pctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	if res := talos.Probe(pctx, m.IP, 2*time.Second); res.Err == nil {
		w.Store.UpsertNode(cluster.RowFromScan(res))
	}
}

type misses struct {
	mu sync.Mutex
	n  map[string]int
}

func (ms *misses) record(key string, up bool) int {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	if up {
		delete(ms.n, key)
		return 0
	}
	ms.n[key]++
	return ms.n[key]
}

func (w *Watcher) pingLoop(ctx context.Context) {
	ms := &misses{n: map[string]int{}}
	w.every(ctx, pingEvery, nil, func(ctx context.Context) { w.ping(ctx, ms) })
}

func (w *Watcher) ping(ctx context.Context, ms *misses) {
	var wg sync.WaitGroup
	for _, m := range w.Store.ListNodes("") {
		if m.IP == "" || m.Kind() == store.KindMember {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			up := talos.PortOpen(ctx, m.IP, 2*time.Second)
			if ctx.Err() != nil {
				return
			}
			n := ms.record(m.MAC, up)
			switch {
			case up && m.Kind() == store.KindOffline:
				w.reprobe(ctx, m)
			case up:
				w.Store.Touch(m.MAC)
			case n >= missesOffline && m.Kind() != store.KindOffline:
				w.Store.SetNodeState(m.IP, "offline")
			}
		}()
	}
	wg.Wait()
}
