package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/mikael/kubit/internal/store"
)

func (s *Server) sopsRoutes() {
	s.mux.HandleFunc("GET /api/v1/clusters/{name}/sops", s.handleSOPSKey)
	s.mux.HandleFunc("GET /api/v1/clusters/{name}/sops/identity", s.handleSOPSExport)
	s.mux.HandleFunc("PUT /api/v1/clusters/{name}/sops/identity", s.handleSOPSImport)
}

func (s *Server) handleSOPSKey(w http.ResponseWriter, r *http.Request) {
	k, err := s.manager.SOPSKey(r.Context(), r.PathValue("name"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, k)
}

func (s *Server) handleSOPSExport(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	k, err := s.manager.SOPSKey(r.Context(), name)
	if err != nil {
		writeErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), name, "sops.export", k.Recipient)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`-age.txt"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Write(k.Identity)
}

func (s *Server) handleSOPSImport(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req struct {
		Keys string `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	k, err := s.manager.ImportSOPSKey(r.Context(), name, []byte(req.Keys))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, err)
		return
	}
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	_ = s.store.Audit(r.Context(), name, "sops.import", k.Recipient)
	writeJSON(w, http.StatusOK, k)
}
