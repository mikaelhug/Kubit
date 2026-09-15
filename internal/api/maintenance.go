package api

import (
	"fmt"
	"net/http"
	"time"
)

// disruptive gates a handler on the cluster's maintenance window. Outside the window
// the request is refused with 409 unless ?force=true; the UI passes force after
// showing the operator the window notice, so the gate protects scripts and habit.
func (s *Server) disruptive(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if name != "" && r.URL.Query().Get("ignoreWindow") != "true" {
			if c, _, err := s.manager.LoadCluster(r.Context(), name); err == nil {
				if open, next := c.Spec.Maintenance.Open(time.Now()); !open {
					msg := fmt.Sprintf("outside the maintenance window (%s", c.Spec.Maintenance.Window)
					if c.Spec.Maintenance.Timezone != "" {
						msg += " " + c.Spec.Maintenance.Timezone
					}
					msg += ")"
					if !next.IsZero() {
						msg += "; next opens " + next.Format("Mon 2006-01-02 15:04 MST")
					}
					writeJSON(w, http.StatusConflict, map[string]string{"error": msg + ". Add ?ignoreWindow=true to override."})
					return
				}
			}
		}
		h(w, r)
	}
}

// handleMaintenance reports the window state for the UI's confirm dialogs.
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
