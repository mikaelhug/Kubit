package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/mikael/kubit/internal/store"
)

func (s *Server) settingsRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/settings", s.handleGetSettings)
	r.HandleFunc("PUT /api/v1/settings", s.handlePutSettings)
	r.HandleFunc("POST /api/v1/settings/alerts/test", s.handleAlertTest)
	r.HandleFunc("GET /api/v1/audit", s.handleAudit)
	r.HandleFunc("GET /api/v1/pxe", s.handlePXEStatus)
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
	if v.Alerts.SMTP.Password != "" {
		v.Alerts.SMTP.Password = "•••"
	}
	if v.Offsite.SecretKey != "" {
		v.Offsite.SecretKey = "•••"
	}
	if v.AMT.Password != "" {
		v.AMT.Password = "•••"
	}
	return v
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var v store.Settings
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		writeErr(w, err)
		return
	}
	if u, err := url.Parse(v.FactoryURL); err != nil || u.Scheme == "" || u.Host == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "factoryUrl must be an absolute URL"})
		return
	}
	if v.WatchIntervalSec < 5 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "watchIntervalSec must be at least 5"})
		return
	}
	if v.Alerts.SMTP.Password == "•••" || v.Offsite.SecretKey == "•••" || v.AMT.Password == "•••" {
		if cur, err := s.store.GetSettings(r.Context()); err == nil {
			if v.Alerts.SMTP.Password == "•••" {
				v.Alerts.SMTP.Password = cur.Alerts.SMTP.Password
			}
			if v.Offsite.SecretKey == "•••" {
				v.Offsite.SecretKey = cur.Offsite.SecretKey
			}
			if v.AMT.Password == "•••" {
				v.AMT.Password = cur.AMT.Password
			}
		}
	}
	if err := s.store.PutSettings(r.Context(), v); err != nil {
		writeErr(w, err)
		return
	}
	s.applySettings(v)
	_ = s.store.Audit(r.Context(), "", "settings.save", "")
	writeJSON(w, http.StatusOK, redactSettings(v))
}

// applySettings pushes live-changeable settings into running components.
func (s *Server) applySettings(v store.Settings) {
	s.manager.Factory.BaseURL = v.FactoryURL
	if s.watcher != nil && v.WatchIntervalSec > 0 {
		s.watcher.Interval = time.Duration(v.WatchIntervalSec) * time.Second
	}
}

// handlePXEStatus proxies the pxe process's status page; the process runs separately
// (it needs root for ports 67/69/4011), so "not running" is a normal answer.
// pxeCommand is the foreground command a terminal user runs; binary path resolved so
// it can be pasted as is.
func pxeCommand(host string) string {
	bin := "kubit"
	if p, err := os.Executable(); err == nil {
		bin = p
	}
	return fmt.Sprintf("sudo %s pxe --iface en0 --kubit-url http://%s", bin, host)
}

// pxeRunning asks the separate PXE process for its status page.
func (s *Server) pxeRunning(ctx contextT) bool {
	v, err := s.store.GetSettings(ctx)
	if err != nil || v.PXEStatusURL == "" {
		return false
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(v.PXEStatusURL)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (s *Server) handlePXEStatus(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.GetSettings(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(v.PXEStatusURL)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"running": false, "statusUrl": v.PXEStatusURL, "error": err.Error(),
			"command": pxeCommand(r.Host), "serviceCommand": fmt.Sprintf("sudo kubit service install --pxe --iface en0 --kubit-url http://%s", r.Host)})
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var st map[string]any
	if err := json.Unmarshal(body, &st); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"running": false, "statusUrl": v.PXEStatusURL, "error": "unexpected response from " + v.PXEStatusURL})
		return
	}
	st["running"] = true
	st["statusUrl"] = v.PXEStatusURL
	writeJSON(w, http.StatusOK, st)
}

// handleBackup streams a sealed backup of the Kubit home.
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Checkpoint(r.Context()); err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="kubit-%s.kubitbak"`, time.Now().Format("20060102-150405")))
	if err := store.Backup(s.manager.Home, s.crypto, w); err != nil {
		// Headers are out; the truncated body is the only signal left.
		fmt.Fprintf(w, "\nBACKUP FAILED: %v\n", err)
	}
	_ = s.store.Audit(r.Context(), "", "backup", "")
}
