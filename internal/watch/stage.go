package watch

import (
	"context"
	"crypto/sha256"
	"log"
	"slices"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/k8s"
)

type stageSource func(ctx context.Context, ip string, talosconfig []byte, report func(stage string)) error

type stageWatch struct {
	ip, host string
	config   [sha256.Size]byte
	seen     string
	cancel   context.CancelFunc
	done     chan struct{}
}

func (sw *stageWatch) ended() bool {
	select {
	case <-sw.done:
		return true
	default:
		return false
	}
}

func (sw *stageWatch) keeps(n cluster.NodeStatus, config [sha256.Size]byte) bool {
	return sw.host == n.Hostname && sw.config == config && !sw.ended() && (sw.seen == "" || sw.seen == n.Stage)
}

func (w *Watcher) syncStageWatches(ctx context.Context, name string, st *cluster.Status, gap bool) {
	want := map[string]cluster.NodeStatus{}
	if !gap && ctx.Err() == nil {
		for _, n := range st.Nodes {
			if n.TalosReachable && n.IP != "" {
				want[n.IP] = n
			}
		}
	}
	var talosconfig []byte
	if len(want) > 0 {
		sec, err := w.Store.GetClusterSecrets(ctx, name)
		if err != nil {
			log.Printf("watch %s: stage watches: %v", name, err)
			clear(want)
		} else {
			talosconfig = sec.Talosconfig
		}
	}
	config := sha256.Sum256(talosconfig)
	w.mu.Lock()
	defer w.mu.Unlock()
	watches := w.stages[name]
	for ip, sw := range watches {
		if n, ok := want[ip]; !ok || !sw.keeps(n, config) {
			sw.cancel()
			delete(watches, ip)
		}
	}
	for ip, n := range want {
		if watches[ip] != nil {
			continue
		}
		if watches == nil {
			watches = map[string]*stageWatch{}
			w.stages[name] = watches
		}
		watches[ip] = w.startStageWatch(ctx, name, n, talosconfig, config)
	}
	if len(watches) == 0 {
		delete(w.stages, name)
	}
}

func (w *Watcher) startStageWatch(ctx context.Context, name string, n cluster.NodeStatus, talosconfig []byte, config [sha256.Size]byte) *stageWatch {
	wctx, cancel := context.WithCancel(ctx)
	sw := &stageWatch{ip: n.IP, host: n.Hostname, config: config, cancel: cancel, done: make(chan struct{})}
	source := w.watchStage
	go func() {
		defer close(sw.done)
		defer cancel()
		err := source(wctx, sw.ip, talosconfig, func(stage string) { w.pushStage(wctx, name, sw, stage) })
		if err != nil && wctx.Err() == nil {
			log.Printf("watch %s: stage of %s: %v", name, sw.host, err)
		}
	}()
	return sw
}

func (w *Watcher) pushStage(ctx context.Context, name string, sw *stageWatch, stage string) {
	w.mu.Lock()
	if ctx.Err() != nil || w.stages[name][sw.ip] != sw {
		w.mu.Unlock()
		return
	}
	sw.seen = stage
	next := withStage(w.last[name], sw, stage)
	if next != nil {
		w.last[name] = next
	}
	w.mu.Unlock()
	if next == nil {
		return
	}
	w.refresh(name, k8s.ScopeNodes)
	if w.OnStatus != nil {
		w.OnStatus(name, next)
	}
}

func withStage(cur *cluster.Status, sw *stageWatch, stage string) *cluster.Status {
	if cur == nil {
		return nil
	}
	i := slices.IndexFunc(cur.Nodes, func(n cluster.NodeStatus) bool { return n.IP == sw.ip && n.Hostname == sw.host })
	if i < 0 || !cur.Nodes[i].TalosReachable || cur.Nodes[i].Stage == stage {
		return nil
	}
	next := *cur
	next.Nodes = slices.Clone(cur.Nodes)
	next.Nodes[i].Stage = stage
	return &next
}

func (w *Watcher) dropStageWatches(name string) {
	for _, sw := range w.stages[name] {
		sw.cancel()
	}
	delete(w.stages, name)
}
