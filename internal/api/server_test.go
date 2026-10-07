package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mikaelhug/kubit/internal/api"
	"github.com/mikaelhug/kubit/internal/cluster"
	"github.com/mikaelhug/kubit/internal/store"
	"github.com/mikaelhug/kubit/internal/watch"
)

func newServer(t *testing.T, token string) (*api.Server, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	s := store.New()
	m := cluster.NewManager(s, dir)
	return api.New("test", m, token, api.DefaultSettings(), watch.New(m, 0), func() {}), s
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

func TestTokenGuardsTheAPIAndTheLiveStream(t *testing.T) {
	srv, _ := newServer(t, "secret")
	if rec := do(t, srv, "GET", "/api/v1/clusters", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d", rec.Code)
	}
	if rec := do(t, srv, "GET", "/api/v1/clusters", "", "Authorization", "Bearer secret"); rec.Code != http.StatusOK {
		t.Errorf("with token: %d", rec.Code)
	}
	if rec := do(t, srv, "GET", "/api/v1/ws", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("ws without token: %d", rec.Code)
	}
	if rec := do(t, srv, "GET", "/api/v1/ws?token=secret", ""); rec.Code == http.StatusUnauthorized {
		t.Errorf("ws with query token must pass the guard, got %d", rec.Code)
	}
}

func TestOnlyLoopbackBindsServeWithoutAToken(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:8080", "192.168.1.5:8080", "bad"} {
		if api.Loopback(addr) {
			t.Errorf("%s counts as loopback", addr)
		}
	}
}
