package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/mikael/kubit/internal/store"
)

func (s *Server) settingsRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/settings", s.handleGetSettings)
	r.HandleFunc("PUT /api/v1/settings", s.handlePutSettings)
	r.HandleFunc("GET /api/v1/backup", s.handleBackup)
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.GetSettings(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, redactSettings(v))
}

func redactSettings(v store.Settings) store.Settings {
	for _, p := range v.Secrets() {
		if *p != "" {
			*p = store.Masked
		}
	}
	return v
}

func (s *Server) unmaskSettings(ctx context.Context, v *store.Settings) {
	next := v.Secrets()
	if !slices.ContainsFunc(next, func(p *string) bool { return *p == store.Masked }) {
		return
	}
	cur, err := s.store.GetSettings(ctx)
	if err != nil {
		return
	}
	for i, p := range cur.Secrets() {
		if *next[i] == store.Masked {
			*next[i] = *p
		}
	}
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var v store.Settings
	if !decodeJSON(w, r, &v) {
		return
	}
	if u, err := url.Parse(v.FactoryURL); err != nil || u.Scheme == "" || u.Host == "" {
		writeErr(w, &statusError{Status: http.StatusUnprocessableEntity, Msg: "factoryUrl must be an absolute URL"})
		return
	}
	if v.WatchIntervalSec < 5 {
		writeErr(w, &statusError{Status: http.StatusUnprocessableEntity, Msg: "watchIntervalSec must be at least 5"})
		return
	}
	s.unmaskSettings(r.Context(), &v)
	if err := s.store.PutSettings(r.Context(), v); err != nil {
		writeErr(w, err)
		return
	}
	s.applySettings(v)
	_ = s.store.Audit(r.Context(), "", "settings.save", "")
	writeJSON(w, http.StatusOK, redactSettings(v))
}

func (s *Server) applySettings(v store.Settings) {
	s.manager.Factory.SetBaseURL(v.FactoryURL)
	if s.watcher != nil && v.WatchIntervalSec > 0 {
		s.watcher.SetInterval(time.Duration(v.WatchIntervalSec) * time.Second)
	}
}

func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="kubit-%s.kubitbak"`, time.Now().Format("20060102-150405")))
	if err := store.Backup(s.manager.Home, s.crypto, w); err != nil {
		fmt.Fprintf(w, "\nBACKUP FAILED: %v\n", err)
	}
	_ = s.store.Audit(r.Context(), "", "backup", "")
}
