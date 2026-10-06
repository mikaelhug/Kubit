package api

import (
	"net/http"
	"strconv"

	"github.com/mikael/kubit/internal/repo"
	"github.com/mikael/kubit/internal/sops"
)

func (s *Server) sopsRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/clusters/{name}/sops", s.handleSOPSKey)
	r.HandleFunc("POST /api/v1/clusters/{name}/sops/flux", s.handleLetFluxDecrypt)
}

func (s *Server) handleSOPSKey(w http.ResponseWriter, r *http.Request) {
	k, err := s.manager.SOPSKey(r.PathValue("name"))
	reply(w, k, err)
}

func (s *Server) handleLetFluxDecrypt(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	dir, err := s.repoOf(name)
	if err != nil {
		writeErr(w, err)
		return
	}
	if q := r.URL.Query().Get("repo"); q != "" {
		i, err := strconv.Atoi(q)
		if err == nil {
			dir, err = s.repoDir(i)
		}
		if err != nil {
			writeErr(w, badRequest("unknown repo"))
			return
		}
	}
	k, err := s.manager.SOPSKey(name)
	if err != nil {
		writeErr(w, conflict(err.Error()))
		return
	}
	ids, err := sops.Identities()
	if err != nil {
		writeErr(w, err)
		return
	}
	n, err := repo.LetFluxDecrypt(dir, k.Recipient, ids)
	s.secretsChanged(dir)
	if err != nil {
		writeErr(w, conflict(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"rekeyed": n})
}
