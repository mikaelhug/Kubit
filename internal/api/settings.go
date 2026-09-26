package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"time"

	"github.com/mikael/kubit/internal/httpx"
	"github.com/mikael/kubit/internal/pxe"
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
		writeErr(w, &statusError{http.StatusUnprocessableEntity, "factoryUrl must be an absolute URL"})
		return
	}
	if v.WatchIntervalSec < 5 {
		writeErr(w, &statusError{http.StatusUnprocessableEntity, "watchIntervalSec must be at least 5"})
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

const pxeStatusWait = 2 * time.Second

func (s *Server) applySettings(v store.Settings) {
	s.manager.Factory.SetBaseURL(v.FactoryURL)
	if s.watcher != nil && v.WatchIntervalSec > 0 {
		s.watcher.SetInterval(time.Duration(v.WatchIntervalSec) * time.Second)
	}
}

func pxeCommand(host string, httpOnly bool) string {
	bin := "kubit"
	if p, err := os.Executable(); err == nil {
		bin = p
	}
	if httpOnly {
		return fmt.Sprintf("%s pxe --http-only --iface en0 --kubit-url http://%s", bin, host)
	}
	return fmt.Sprintf("sudo %s pxe --iface en0 --kubit-url http://%s", bin, host)
}

func pxeDown(w http.ResponseWriter, cmd string) {
	writeJSON(w, http.StatusConflict, map[string]string{"error": "The PXE server is not running, so the machine would find nothing to boot. Start it in a terminal (it can stay open): " + cmd, "code": "pxe-down", "command": cmd})
}

func (s *Server) pxeFetch(ctx context.Context) (string, []byte, error) {
	v, err := s.store.GetSettings(ctx)
	if err != nil {
		return "", nil, err
	}
	if v.PXEStatusURL == "" {
		return "", nil, errors.New("no PXE status URL")
	}
	body, err := fetchPXE(ctx, v.PXEStatusURL)
	return v.PXEStatusURL, body, err
}

func fetchPXE(ctx context.Context, statusURL string) ([]byte, error) {
	resp, err := httpx.Get(ctx, statusURL, pxeStatusWait)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", statusURL, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

func (s *Server) pxeStatus(ctx context.Context) (*pxe.Status, error) {
	_, body, err := s.pxeFetch(ctx)
	if err != nil {
		return nil, err
	}
	var st pxe.Status
	if err := json.Unmarshal(body, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

func (s *Server) pxeRunning(ctx context.Context) bool {
	_, _, err := s.pxeFetch(ctx)
	return err == nil
}

func (s *Server) handlePXEStatus(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.GetSettings(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	body, err := fetchPXE(r.Context(), v.PXEStatusURL)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"running": false, "statusUrl": v.PXEStatusURL, "error": err.Error(),
			"command": pxeCommand(r.Host, false), "serviceCommand": fmt.Sprintf("sudo kubit service install --pxe --iface en0 --kubit-url http://%s", r.Host)})
		return
	}
	var st map[string]any
	if err := json.Unmarshal(body, &st); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"running": false, "statusUrl": v.PXEStatusURL, "error": "unexpected response from " + v.PXEStatusURL})
		return
	}
	st["running"] = true
	st["statusUrl"] = v.PXEStatusURL
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Checkpoint(r.Context()); err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="kubit-%s.kubitbak"`, time.Now().Format("20060102-150405")))
	if err := store.Backup(s.manager.Home, s.crypto, w); err != nil {
		fmt.Fprintf(w, "\nBACKUP FAILED: %v\n", err)
	}
	_ = s.store.Audit(r.Context(), "", "backup", "")
}
