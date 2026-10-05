package api_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mikael/kubit/internal/api"
	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/store"
)

func newServer(t *testing.T, token string) (*api.Server, *store.Store) {
	t.Helper()
	c, _ := store.NewCrypto(bytes.Repeat([]byte{3}, 32))
	dir := t.TempDir()
	s, err := store.Open(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return api.New("test", cluster.NewManager(s, dir), token), s
}

func do(t *testing.T, h http.Handler, method, path string, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:40000"
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestTokenGuardsAPIOnly(t *testing.T) {
	srv, _ := newServer(t, "secret")
	if rec := do(t, srv, "GET", "/api/v1/clusters", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d", rec.Code)
	}
	if rec := do(t, srv, "GET", "/api/v1/clusters", "", "Authorization", "Bearer secret"); rec.Code != http.StatusOK {
		t.Errorf("with token: %d", rec.Code)
	}
	if rec := do(t, srv, "GET", "/api/v1/version", ""); rec.Code != http.StatusOK {
		t.Errorf("version is open so the UI can greet before sign-in: %d", rec.Code)
	}
	if rec := do(t, srv, "GET", "/api/v1/ws", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("ws without token: %d", rec.Code)
	}
	if rec := do(t, srv, "GET", "/api/v1/ws?token=secret", ""); rec.Code == http.StatusUnauthorized {
		t.Errorf("ws with query token must pass the guard, got %d", rec.Code)
	}
	if rec := do(t, srv, "GET", "/", ""); rec.Code != http.StatusOK {
		t.Errorf("SPA must not require the token: %d", rec.Code)
	}
}

func TestLoopback(t *testing.T) {
	for addr, want := range map[string]bool{"127.0.0.1:8080": true, "localhost:8080": true, "[::1]:8080": true, "0.0.0.0:8080": false, "192.168.1.5:8080": false, "bad": false} {
		if got := api.Loopback(addr); got != want {
			t.Errorf("Loopback(%q) = %v", addr, got)
		}
	}
}

func TestUnknownAPIPathIsNotFound(t *testing.T) {
	srv, _ := newServer(t, "")
	rec := do(t, srv, "POST", "/api/v1/clusters", `{}`)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"error"`) {
		t.Errorf("removed route: %d %s", rec.Code, rec.Body)
	}
}
