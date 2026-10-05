package api

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	return New("test", m, "", c), st
}

func call(t *testing.T, s *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:40000"
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func waitOp(t *testing.T, st *store.Store, id int64) *store.OperationRow {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		op, err := st.GetOperation(context.Background(), id)
		if err == nil && op.Status != "running" {
			return op
		}
		time.Sleep(20 * time.Millisecond)
	}
	op, _ := st.GetOperation(context.Background(), id)
	t.Fatalf("operation %d still running: %s", id, op.Log)
	return nil
}
