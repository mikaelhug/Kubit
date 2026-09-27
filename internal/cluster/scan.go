package cluster

import (
	"context"
	"encoding/json"

	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
)

func RowFromScan(res talos.ScanResult) store.NodeRow {
	row := store.NodeRow{IP: res.IP, Source: "scan", State: string(res.State)}
	if inv := res.Inventory; inv != nil {
		row.MAC, row.Arch, row.TalosVersion = inv.PrimaryMAC(), inv.Arch, inv.TalosVersion
		row.UUID, row.Serial = inv.UUID, inv.Serial
		row.Hardware, _ = json.Marshal(inv)
	}
	return row
}

func RecordScan(ctx context.Context, st *store.Store, results []talos.ScanResult, note func(res talos.ScanResult, vipOf string)) (int, error) {
	vips := st.ClusterVIPs(ctx)
	found := 0
	for _, res := range results {
		if res.Err != nil {
			continue
		}
		name, isVIP := vips[res.IP]
		if !isVIP {
			if err := st.UpsertNode(ctx, RowFromScan(res)); err != nil {
				return found, err
			}
			found++
		}
		if note != nil {
			note(res, name)
		}
	}
	return found, nil
}
