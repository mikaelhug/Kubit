package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/mikael/kubit/internal/httpx"
	"github.com/mikael/kubit/internal/pxe"
	"github.com/mikael/kubit/internal/store"
)

func (s *Server) pxeRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/pxe", s.handlePXEStatus)
	r.HandleFunc("GET /api/v1/pxe/decide", s.handlePXEDecide)
}

const pxeStatusWait = 2 * time.Second

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
	writeErr(w, &statusError{Status: http.StatusConflict, Msg: "The PXE server is not running, so the machine would find nothing to boot. Start it in a terminal (it can stay open): " + cmd, Code: "pxe-down", Command: cmd})
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
	return parsePXE(body)
}

func parsePXE(body []byte) (*pxe.Status, error) {
	var st pxe.Status
	if err := json.Unmarshal(body, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

func (s *Server) pxeServing(w http.ResponseWriter, r *http.Request, httpOnly bool) (*pxe.Status, bool) {
	_, body, err := s.pxeFetch(r.Context())
	var st *pxe.Status
	if err == nil {
		st, err = parsePXE(body)
	}
	if err != nil {
		pxeDown(w, pxeCommand(r.Host, httpOnly))
		return nil, false
	}
	if st.IP == "" {
		writeErr(w, &statusError{Status: http.StatusConflict, Msg: fmt.Sprintf("The PXE server has no address on %s.", st.Interface), Code: "pxe-no-address"})
		return nil, false
	}
	return st, true
}

type pxeSnapshot struct {
	url  string
	body []byte
	err  error
	at   time.Time
}

const pxeSnapshotFresh = 15 * time.Second

func (s *Server) latestPXE(ctx context.Context, statusURL string) ([]byte, error) {
	s.pxeMu.Lock()
	last := s.pxeLast
	s.pxeMu.Unlock()
	if last.url == statusURL && time.Since(last.at) < pxeSnapshotFresh {
		return last.body, last.err
	}
	return fetchPXE(ctx, statusURL)
}

func (s *Server) handlePXEStatus(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.GetSettings(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	body, err := s.latestPXE(r.Context(), v.PXEStatusURL)
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

func (s *Server) handlePXEDecide(w http.ResponseWriter, r *http.Request) {
	mac := queryMAC(r)
	boot, reason := s.pxeDecision(r.Context(), mac)
	writeJSON(w, http.StatusOK, map[string]string{"boot": boot, "reason": reason})
}

func (s *Server) pxeDecision(ctx context.Context, mac string) (string, string) {
	m, err := s.store.GetMachine(ctx, mac)
	if err != nil {
		v, _ := s.store.GetSettings(ctx)
		if v.PXEEnrollment == "closed" {
			return "local", "unknown machine and enrollment is closed"
		}
		return "talos", "unknown machine, enrollment open"
	}
	if boot, ok := labBoot(m); ok {
		return boot, "armed as lab host: Debian installer"
	}
	switch {
	case m.Kind() == store.KindLabHost:
		return "local", "lab host"
	case m.Provision:
		return "talos", "armed with Boot into Talos"
	case m.Cluster != "":
		return "local", "member of cluster " + m.Cluster
	default:
		return "talos", "known, unassigned machine"
	}
}

func (s *Server) watchPXE(ctx context.Context) {
	var last string
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		statusURL, b, err := s.pxeFetch(ctx)
		if statusURL == "" {
			continue
		}
		s.pxeMu.Lock()
		s.pxeLast = pxeSnapshot{url: statusURL, body: b, err: err, at: time.Now()}
		s.pxeMu.Unlock()
		if body := string(b); body != last {
			last = body
			s.refresh("", "pxe")
		}
	}
}
