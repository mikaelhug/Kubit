package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/mikael/kubit/internal/repo"
	"github.com/mikael/kubit/internal/sops"
)

func (s *Server) sopsRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/clusters/{name}/sops", s.handleSOPSKey)
	r.HandleFunc("POST /api/v1/clusters/{name}/sops/flux", s.handleLetFluxDecrypt)
	r.HandleFunc("GET /api/v1/clusters/{name}/flux/key", s.handleDeployKey)
	r.HandleFunc("POST /api/v1/clusters/{name}/flux/key", s.handleNewDeployKey)
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

func (s *Server) handleDeployKey(w http.ResponseWriter, r *http.Request) {
	dir, err := s.repoOf(r.PathValue("name"))
	if err != nil {
		writeErr(w, err)
		return
	}
	ids, err := sops.Identities()
	if err != nil {
		writeErr(w, err)
		return
	}
	k, err := repo.DeployKeyOf(dir, ids)
	reply(w, k, err)
}

func (s *Server) handleNewDeployKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Hash      string `json:"hash"`
		HostsOnly bool   `json:"hostsOnly"`
	}
	if !decode(w, r, &req) {
		return
	}
	dir, err := s.repoOf(r.PathValue("name"))
	if err != nil {
		writeErr(w, err)
		return
	}
	ids, err := sops.Identities()
	if err != nil {
		writeErr(w, err)
		return
	}
	err = repo.NewDeployKey(r.Context(), dir, req.Hash, req.HostsOnly, ids)
	switch {
	case errors.Is(err, repo.ErrNotSSH):
		writeErr(w, conflict(err.Error()))
		return
	case err != nil:
		writeErr(w, editErr(err))
		return
	}
	k, err := repo.DeployKeyOf(dir, ids)
	reply(w, k, err)
}
