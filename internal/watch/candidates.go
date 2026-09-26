package watch

import (
	"context"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/oob"
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
