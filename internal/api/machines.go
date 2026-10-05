package api

import (
	"encoding/json"

	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
)

func inventoryOf(m *store.Machine) (talos.Inventory, bool) {
	var inv talos.Inventory
	if len(m.Hardware) <= 2 || json.Unmarshal(m.Hardware, &inv) != nil {
		return inv, false
	}
	return inv, true
}
