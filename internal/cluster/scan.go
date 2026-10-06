package cluster

import (
	"encoding/json"

	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
)

func RowFromScan(res talos.ScanResult) store.Machine {
	row := store.Machine{IP: res.IP, State: string(res.State)}
	if inv := res.Inventory; inv != nil {
		row.MAC, row.Arch, row.TalosVersion = inv.PrimaryMAC(), inv.Arch, inv.TalosVersion
		row.UUID, row.Serial = inv.UUID, inv.Serial
		row.Hardware, _ = json.Marshal(inv)
	}
	return row
}

func RecordScan(st *store.Store, results []talos.ScanResult) int {
	vips := st.ClusterVIPs()
	found := 0
	for _, res := range results {
		if _, isVIP := vips[res.IP]; res.Err == nil && !isVIP {
			st.UpsertNode(RowFromScan(res))
			found++
		}
	}
	return found
}
