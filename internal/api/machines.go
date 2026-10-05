package api

import (
	"encoding/json"
	"net/http"

	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
)

func (s *Server) machineRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/machines", s.handleNodes)
	r.HandleFunc("GET /api/v1/machines/{mac}", s.handleMachine)
}

func (s *Server) handleMachine(w http.ResponseWriter, r *http.Request) {
	m, err := s.store.GetMachine(r.Context(), r.PathValue("mac"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, machineView(*m))
}

func inventoryOf(m *store.Machine) (talos.Inventory, bool) {
	var inv talos.Inventory
	if len(m.Hardware) <= 2 || json.Unmarshal(m.Hardware, &inv) != nil {
		return inv, false
	}
	return inv, true
}
