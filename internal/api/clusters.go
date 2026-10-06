package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/repo"
)

func (s *Server) clusterRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/clusters", s.handleClusters)
	r.HandleFunc("GET /api/v1/clusters/{name}/status", s.handleClusterStatus)
	r.HandleFunc("GET /api/v1/clusters/{name}/yaml", s.handleClusterYAML)
	r.HandleFunc("PUT /api/v1/clusters/{name}/yaml", s.handleClusterYAMLPut)
	r.HandleFunc("GET /api/v1/clusters/{name}/kubeconfig", s.handleClusterKubeconfig)
	r.HandleFunc("GET /api/v1/clusters/{name}/image", s.handleImageStatus)
	r.HandleFunc("GET /api/v1/clusters/{name}/config", s.handleConfigStatus)
}

func (s *Server) handleClusters(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.store.ListClusters())
}

func (s *Server) handleClusterStatus(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("fresh") != "true" {
		if st := s.watcher.Latest(r.PathValue("name")); st != nil {
			writeJSON(w, http.StatusOK, st)
			return
		}
	}
	st, err := s.manager.Status(r.Context(), r.PathValue("name"))
	reply(w, st, err)
}

func (s *Server) handleClusterYAML(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	for _, sr := range s.servedRepos() {
		if sr.Cluster != name {
			continue
		}
		b, err := os.ReadFile(filepath.Join(sr.Dir, repo.ClusterFile))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"dir": sr.Dir, "yaml": string(b), "hash": repo.Fingerprint(b)})
		return
	}
	writeErr(w, &statusError{Status: http.StatusNotFound, Msg: "not served from a repo; start Kubit with the cluster's repo dir"})
}

func (s *Server) handleClusterYAMLPut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		YAML string `json:"yaml"`
		Hash string `json:"hash"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, badRequest("body must be {yaml, hash}"))
		return
	}
	name := r.PathValue("name")
	dir, err := s.repoOf(name)
	if err != nil {
		writeErr(w, err)
		return
	}
	err = repo.WriteSpec(dir, req.Hash, []byte(req.YAML), func(c *config.Cluster) error {
		if c.Metadata.Name != name {
			return fmt.Errorf("metadata.name must stay %q; a new name is a new cluster", name)
		}
		return nil
	})
	if err != nil {
		writeErr(w, editErr(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"hash": repo.Fingerprint([]byte(req.YAML))})
}

func (s *Server) handleClusterKubeconfig(w http.ResponseWriter, r *http.Request) {
	sec, err := s.store.GetClusterSecrets(r.PathValue("name"))
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	w.Header().Set("Content-Disposition", `attachment; filename="kubeconfig"`)
	w.Write(sec.Kubeconfig)
}

func (s *Server) handleImageStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.manager.ImageStatus(r.Context(), r.PathValue("name"))
	reply(w, st, err)
}

func (s *Server) handleConfigStatus(w http.ResponseWriter, r *http.Request) {
	if _, err := s.store.GetCluster(r.PathValue("name")); err != nil {
		writeErr(w, err)
		return
	}
	if _, p := s.latestPlan(r.PathValue("name")); p != nil {
		writeJSON(w, http.StatusOK, p.ConfigStatus())
		return
	}
	writeJSON(w, http.StatusOK, cluster.ConfigStatus{Behind: []string{}})
}
