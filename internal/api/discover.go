package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/talos"
)

func (s *Server) discoverRoutes() {
	s.mux.HandleFunc("POST /api/v1/discover", s.handleDiscover)
}

type discoverRequest struct {
	Targets []string `json:"targets"`
}

func (s *Server) handleDiscover(w http.ResponseWriter, r *http.Request) {
	var req discoverRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Targets) == 0 {
		writeErr(w, badRequest(`body must be {"targets": ["cidr or ip", ...]}`))
		return
	}
	addrs, err := talos.ExpandTargets(req.Targets)
	if err != nil {
		writeErr(w, invalid(err))
		return
	}
	found, err := cluster.RecordScan(r.Context(), s.store, talos.Scan(r.Context(), addrs, 64, 2*time.Second), nil)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"found": found})
}
