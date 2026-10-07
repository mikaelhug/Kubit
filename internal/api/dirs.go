package api

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mikaelhug/kubit/internal/repo"
)

type dirEntry struct {
	Name    string `json:"name"`
	Git     bool   `json:"git,omitempty"`
	Cluster bool   `json:"cluster,omitempty"`
}

type dirListing struct {
	Path   string     `json:"path"`
	Parent string     `json:"parent,omitempty"`
	Dirs   []dirEntry `json:"dirs"`
}

func (s *Server) handleDirs(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if p == "" {
		p = "~"
	}
	dir, err := expandHome(p)
	if err != nil {
		writeErr(w, badRequest(err.Error()))
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		writeErr(w, badRequest(err.Error()))
		return
	}
	out := dirListing{Path: repo.Tilde(dir), Dirs: []dirEntry{}}
	if parent := filepath.Dir(dir); parent != dir {
		out.Parent = repo.Tilde(parent)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") || !isDir(filepath.Join(dir, e.Name())) {
			continue
		}
		sub := filepath.Join(dir, e.Name())
		out.Dirs = append(out.Dirs, dirEntry{Name: e.Name(), Git: exists(filepath.Join(sub, ".git")), Cluster: exists(filepath.Join(sub, repo.ClusterFile))})
	}
	slices.SortFunc(out.Dirs, func(a, b dirEntry) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	writeJSON(w, http.StatusOK, out)
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
