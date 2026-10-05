package api

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/store"
)

func localServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	c, _ := store.NewCrypto(bytes.Repeat([]byte{11}, 32))
	dir := t.TempDir()
	st, err := store.Open(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	factory := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/versions":
			fmt.Fprint(w, `["v1.14.0","v1.14.1"]`)
		case "/schematics":
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"id":"d9ff89777e246792e7642abd3220a616afb4e49822382e4213a2e528ab826fe5"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(factory.Close)
	m := cluster.NewManager(st, dir)
	m.Factory.SetBaseURL(factory.URL)
	return New("test", m, ""), st
}

func call(t *testing.T, s *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:40000"
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}
