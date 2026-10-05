package api

import (
	"net/http"
)

func (s *Server) sopsRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/clusters/{name}/sops", s.handleSOPSKey)
}

func (s *Server) handleSOPSKey(w http.ResponseWriter, r *http.Request) {
	k, err := s.manager.SOPSKey(r.Context(), r.PathValue("name"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, k)
}
