package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/mikaelhug/kubit/internal/cluster"
	"github.com/mikaelhug/kubit/internal/config"
	"github.com/mikaelhug/kubit/internal/repo"
	"github.com/mikaelhug/kubit/internal/store"
	"github.com/mikaelhug/kubit/internal/watch"
)

func localServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st := store.New()
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
	return New("test", m, "", DefaultSettings(), watch.New(m, 0), func() {}), st
}

func call(t *testing.T, s *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:40000"
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

const labSpec = "apiVersion: kubit.dev/v1\nkind: Cluster\nmetadata: {name: lab}\nspec:\n  nodes:\n    - {hostname: cp-01, ip: \"::1\", role: controlplane, installDisk: {path: /dev/sda}}\n"

func servedLab(t *testing.T) (*Server, string) {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SOPS_AGE_KEY", id.String())
	t.Setenv("SOPS_AGE_KEY_FILE", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	c, err := config.Parse([]byte(labSpec))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "lab")
	if _, err := repo.Init(dir, c, []string{id.Recipient().String()}); err != nil {
		t.Fatal(err)
	}
	s, _ := localServer(t)
	if err := s.ServeRepos(t.Context(), []string{dir}); err != nil {
		t.Fatal(err)
	}
	return s, dir
}
