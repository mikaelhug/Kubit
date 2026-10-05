package watch

import (
	"context"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
)

func (w *Watcher) candidateLoop(ctx context.Context) {
	w.every(ctx, w.ServiceInterval, w.candidateTick)
}

func (w *Watcher) candidateTick(ctx context.Context) {
	rows, err := w.Store.ListNodes(ctx, "")
	if err != nil {
		return
	}
	for _, m := range rows {
		if m.IP == "" {
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
		}
	}
}
