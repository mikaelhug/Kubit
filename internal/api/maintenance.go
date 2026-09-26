package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/mikael/kubit/internal/config"
)

func (s *Server) disruptive(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if name != "" && r.URL.Query().Get("ignoreWindow") != "true" {
			if msg, closed := s.windowClosed(r.Context(), name); closed {
				writeJSON(w, http.StatusConflict, map[string]string{"error": msg})
				return
			}
		}
		h(w, r)
	}
}

func (s *Server) windowClosed(ctx context.Context, name string) (string, bool) {
	c, _, err := s.manager.LoadCluster(ctx, name)
	if err != nil {
		return "", false
	}
	open, next := c.Spec.Maintenance.Open(time.Now())
	if open {
		return "", false
	}
	return windowMessage(c.Spec.Maintenance, next), true
}

func windowMessage(m config.Maintenance, next time.Time) string {
	msg := fmt.Sprintf("outside the maintenance window (%s", m.Window)
	if m.Timezone != "" {
		msg += " " + m.Timezone
	}
	msg += ")"
	if !next.IsZero() {
		msg += "; next opens " + next.Format("Mon 2006-01-02 15:04 MST")
	}
	return msg + ". Add ?ignoreWindow=true to override."
}

func (s *Server) handleMaintenance(w http.ResponseWriter, r *http.Request) {
	c, _, err := s.manager.LoadCluster(r.Context(), r.PathValue("name"))
	if err != nil {
		writeErr(w, err)
		return
	}
	open, next := c.Spec.Maintenance.Open(time.Now())
	out := map[string]any{"window": c.Spec.Maintenance.Window, "timezone": c.Spec.Maintenance.Timezone, "open": open}
	if !next.IsZero() {
		out["next"] = next.Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, out)
}
