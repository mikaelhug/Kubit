package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/mikael/kubit/internal/httpx"
)

func (s *Server) pxeRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/pxe", s.handlePXEStatus)
}

const pxeStatusWait = 2 * time.Second

func (s *Server) pxeCommand() string {
	bin := "kubit"
	if p, err := os.Executable(); err == nil {
		bin = p
	}
	cmd := "sudo " + bin + " pxe"
	for _, r := range s.servedRepos() {
		cmd += " " + r.Dir
	}
	return cmd
}

func fetchPXE(ctx context.Context, statusURL string) ([]byte, error) {
	if statusURL == "" {
		return nil, errors.New("no PXE status URL")
	}
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

type pxeSnapshot struct {
	body []byte
	err  error
	at   time.Time
}

var errPXEStopped = errors.New("not running")

func (s *Server) latestPXE(ctx context.Context) ([]byte, error) {
	s.pxeMu.Lock()
	last := s.pxeLast
	s.pxeMu.Unlock()
	if !last.at.IsZero() {
		return last.body, last.err
	}
	return fetchPXE(ctx, s.settings.PXEStatusURL)
}

func (s *Server) handlePXEStatus(w http.ResponseWriter, r *http.Request) {
	statusURL := s.settings.PXEStatusURL
	body, err := s.latestPXE(r.Context())
	if err != nil {
		off := map[string]any{"running": false, "statusUrl": statusURL, "command": s.pxeCommand()}
		if !errors.Is(err, syscall.ECONNREFUSED) && !errors.Is(err, errPXEStopped) {
			off["error"] = err.Error()
		}
		writeJSON(w, http.StatusOK, off)
		return
	}
	var st map[string]any
	if err := json.Unmarshal(body, &st); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"running": false, "statusUrl": statusURL, "error": "unexpected response from " + statusURL, "command": s.pxeCommand()})
		return
	}
	st["running"] = true
	st["statusUrl"] = statusURL
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) watchPXE(ctx context.Context) {
	if s.settings.PXEStatusURL == "" {
		return
	}
	starts := make(chan struct{}, 1)
	start := func() {
		select {
		case starts <- struct{}{}:
		default:
		}
	}
	s.watchPXERunFile(ctx, start)
	start()
	for {
		select {
		case <-ctx.Done():
			return
		case <-starts:
		}
		s.followPXE(ctx)
		s.setPXE(nil, errPXEStopped)
	}
}

func (s *Server) watchPXERunFile(ctx context.Context, start func()) {
	dir := s.settings.PXERunDir
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return
	}
	if err := w.Add(dir); err != nil {
		w.Close()
		return
	}
	go func() {
		defer w.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case e, ok := <-w.Events:
				if !ok {
					return
				}
				if filepath.Base(e.Name) == pxeRunFile && e.Has(fsnotify.Create|fsnotify.Write) {
					start()
				}
			case _, ok := <-w.Errors:
				if !ok {
					return
				}
			}
		}
	}()
}

const (
	pxeRunFile     = "pxe"
	pxeConnectTry  = 5
	pxeConnectWait = 300 * time.Millisecond
)

func (s *Server) followPXE(ctx context.Context) {
	for try := 0; try < pxeConnectTry; try++ {
		if s.streamPXE(ctx) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(pxeConnectWait):
		}
	}
}

func (s *Server) streamPXE(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.settings.PXEStatusURL+"?watch", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
			s.setPXE([]byte(data), nil)
		}
	}
	return true
}

func (s *Server) setPXE(body []byte, err error) {
	s.pxeMu.Lock()
	changed := string(s.pxeLast.body) != string(body) || (s.pxeLast.err == nil) != (err == nil) || s.pxeLast.at.IsZero()
	s.pxeLast = pxeSnapshot{body: body, err: err, at: time.Now()}
	s.pxeMu.Unlock()
	if changed {
		s.refresh("", "pxe")
	}
}
