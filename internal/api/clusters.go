package api

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/store"
)

func (s *Server) clusterRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/clusters", s.handleClusters)
	r.HandleFunc("GET /api/v1/clusters/{name}/status", s.handleClusterStatus)
	r.HandleFunc("GET /api/v1/clusters/{name}/yaml", s.handleClusterYAML)
	r.HandleFunc("GET /api/v1/clusters/{name}/kubeconfig", s.handleClusterKubeconfig)
	r.HandleFunc("GET /api/v1/clusters/{name}/image", s.handleImageStatus)
	r.HandleFunc("GET /api/v1/clusters/{name}/config", s.handleConfigStatus)
}

type clusterSummary struct {
	store.ClusterRow
	Spec *config.Cluster `json:"spec"`
}

func summarize(row store.ClusterRow) *clusterSummary {
	c, _ := config.Parse(row.Spec)
	return &clusterSummary{ClusterRow: row, Spec: c}
}

func (s *Server) handleClusters(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.ListClusters(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	out := []*clusterSummary{}
	for _, row := range rows {
		out = append(out, summarize(row))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleClusterStatus(w http.ResponseWriter, r *http.Request) {
	if s.watcher != nil && r.URL.Query().Get("fresh") != "true" {
		if st := s.watcher.Latest(r.PathValue("name")); st != nil {
			writeJSON(w, http.StatusOK, st)
			return
		}
	}
	st, err := s.manager.Status(r.Context(), r.PathValue("name"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleClusterYAML(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	for _, repo := range s.servedRepos() {
		if repo.Cluster != name {
			continue
		}
		b, err := os.ReadFile(filepath.Join(repo.Dir, "cluster.yaml"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"dir": repo.Dir, "yaml": string(b)})
		return
	}
	writeErr(w, &statusError{Status: http.StatusNotFound, Msg: "not served from a repo; start Kubit with the cluster's repo dir"})
}

func (s *Server) handleClusterKubeconfig(w http.ResponseWriter, r *http.Request) {
	sec, err := s.store.GetClusterSecrets(r.Context(), r.PathValue("name"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if sec.Kubeconfig == nil {
		writeErr(w, &statusError{Status: http.StatusNotFound, Msg: "no kubeconfig yet"})
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	w.Header().Set("Content-Disposition", `attachment; filename="kubeconfig"`)
	w.Write(sec.Kubeconfig)
}

func (s *Server) handleImageStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.manager.ImageStatus(r.Context(), r.PathValue("name"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleConfigStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.manager.ConfigStatus(r.Context(), r.PathValue("name"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}
