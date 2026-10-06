package api

import (
	"net/http"
)

func (s *Server) addonRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/clusters/{name}/addons", s.handleAddons)
}

func (s *Server) handleAddons(w http.ResponseWriter, r *http.Request) {
	list, err := s.manager.Addons(r.Context(), r.PathValue("name"))
	reply(w, list, err)
}
