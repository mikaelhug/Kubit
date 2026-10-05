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
	w.scanSubnets(ctx)
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
			} else if ctx.Err() == nil {
				_ = w.Store.SetNodeState(ctx, m.IP, "offline")
			}
		}
	}
}

func (w *Watcher) scanSubnets(ctx context.Context) {
	if w.Subnets == nil {
		return
	}
	targets := w.Subnets(ctx)
	if len(targets) == 0 {
		return
	}
	addrs, err := talos.ExpandTargets(targets)
	if err != nil {
		return
	}
	var found []talos.ScanResult
	for _, r := range talos.Scan(ctx, addrs, 64, 2*time.Second) {
		if r.Err == nil && r.State == talos.StateMaintenance {
			found = append(found, r)
		}
	}
	_, _ = cluster.RecordScan(ctx, w.Store, found, nil)
}
