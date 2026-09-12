package api

import (
	"encoding/json"
	"io/fs"
	"net/http"

	"github.com/mikael/kubit/web"
)

type Server struct {
	mux     *http.ServeMux
	version string
}

func New(version string) *Server {
	s := &Server{mux: http.NewServeMux(), version: version}
	s.mux.HandleFunc("GET /api/v1/version", s.handleVersion)
	dist, _ := fs.Sub(web.Dist, "dist")
	s.mux.Handle("/", spaHandler(http.FS(dist)))
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]string{"kubit": s.version})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// spaHandler serves static assets and falls back to index.html for client-side routes.
func spaHandler(root http.FileSystem) http.Handler {
	files := http.FileServer(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f, err := root.Open(r.URL.Path); err == nil {
			f.Close()
			files.ServeHTTP(w, r)
			return
		}
		r.URL.Path = "/"
		files.ServeHTTP(w, r)
	})
}
