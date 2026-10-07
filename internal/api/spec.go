package api

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/mikaelhug/kubit/internal/repo"
)

func (s *Server) specRoutes() {
	r := s.mux
	r.HandleFunc("PUT /api/v1/clusters/{name}/platform/{addon}", s.handleAddonPut)
	r.HandleFunc("PUT /api/v1/clusters/{name}/versions", s.handleVersionsPut)
}

func (s *Server) handleAddonPut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		repo.AddonEdit
		Hash string `json:"hash"`
	}
	if !decode(w, r, &req) {
		return
	}
	dir, err := s.repoOf(r.PathValue("name"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := repo.SetAddon(dir, req.Hash, r.PathValue("addon"), req.AddonEdit); err != nil {
		writeErr(w, editErr(err))
		return
	}
	writeHash(w, dir)
}

func (s *Server) handleVersionsPut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Talos      string `json:"talosVersion"`
		Kubernetes string `json:"kubernetesVersion"`
		Hash       string `json:"hash"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Talos == "" && req.Kubernetes == "" {
		writeErr(w, badRequest("body must be {talosVersion, kubernetesVersion}"))
		return
	}
	dir, err := s.repoOf(r.PathValue("name"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := repo.SetVersions(dir, req.Hash, req.Talos, req.Kubernetes); err != nil {
		writeErr(w, editErr(err))
		return
	}
	writeHash(w, dir)
}

func writeHash(w http.ResponseWriter, dir string) {
	b, err := os.ReadFile(filepath.Join(dir, repo.ClusterFile))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"hash": repo.Fingerprint(b)})
}
