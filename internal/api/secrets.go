package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/mikael/kubit/internal/repo"
	"github.com/mikael/kubit/internal/sops"
	"go.yaml.in/yaml/v4"
)

func (s *Server) secretRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/secrets", s.handleSecrets)
	r.HandleFunc("GET /api/v1/secrets/value", s.handleSecretGet)
	r.HandleFunc("PUT /api/v1/secrets/value", s.handleSecretPut)
	r.HandleFunc("DELETE /api/v1/secrets/value", s.handleSecretDelete)
	r.HandleFunc("POST /api/v1/secrets/files", s.handleSecretFile)
}

type secretRepo struct {
	servedRepo
	Index int               `json:"index"`
	Files []repo.SecretFile `json:"files"`
}

func (s *Server) handleSecrets(w http.ResponseWriter, _ *http.Request) {
	out := []secretRepo{}
	for i, r := range s.servedRepos() {
		files, err := repo.SecretFiles(r.Dir)
		sr := secretRepo{servedRepo: r, Index: i, Files: files}
		if sr.Files == nil {
			sr.Files = []repo.SecretFile{}
		}
		if err != nil && sr.Error == "" {
			sr.Error = err.Error()
		}
		out = append(out, sr)
	}
	writeJSON(w, http.StatusOK, out)
}

type secretRef struct {
	Repo  int      `json:"repo"`
	File  string   `json:"file"`
	Key   []string `json:"key"`
	Value string   `json:"value"`
}

func (s *Server) secretFile(ref secretRef) (string, string, error) {
	repos := s.servedRepos()
	if ref.Repo < 0 || ref.Repo >= len(repos) {
		return "", "", badRequest("unknown repo")
	}
	path, err := repo.SecretPath(repos[ref.Repo].Dir, ref.File)
	if err != nil {
		return "", "", badRequest(err.Error())
	}
	return repos[ref.Repo].Dir, path, nil
}

func refFromQuery(r *http.Request) secretRef {
	q := r.URL.Query()
	i, err := strconv.Atoi(q.Get("repo"))
	if err != nil {
		i = -1
	}
	return secretRef{Repo: i, File: q.Get("file"), Key: q["key"]}
}

func (s *Server) handleSecretGet(w http.ResponseWriter, r *http.Request) {
	ref := refFromQuery(r)
	_, path, err := s.secretFile(ref)
	if err != nil {
		writeErr(w, err)
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		writeErr(w, err)
		return
	}
	ids, err := sops.Identities()
	if err != nil {
		writeErr(w, err)
		return
	}
	v, err := sops.Get(b, ids, ref.Key)
	if err != nil {
		writeErr(w, conflict(err.Error()))
		return
	}
	_ = s.store.Audit(r.Context(), "", "secret.read", ref.File+"#"+strings.Join(ref.Key, "."))
	writeJSON(w, http.StatusOK, map[string]string{"value": v})
}

func (s *Server) editSecret(w http.ResponseWriter, r *http.Request, ref secretRef, action string, edit func(*yaml.Node) error) {
	_, path, err := s.secretFile(ref)
	if err != nil {
		writeErr(w, err)
		return
	}
	if len(ref.Key) == 0 || slices.ContainsFunc(ref.Key, func(k string) bool { return strings.TrimSpace(k) == "" }) {
		writeErr(w, badRequest("key is required"))
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		writeErr(w, err)
		return
	}
	ids, err := sops.Identities()
	if err != nil {
		writeErr(w, err)
		return
	}
	out, err := sops.Edit(b, ids, edit)
	if err != nil {
		writeErr(w, conflict(err.Error()))
		return
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		writeErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), "", action, ref.File+"#"+strings.Join(ref.Key, "."))
	s.refresh("", "secrets")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSecretPut(w http.ResponseWriter, r *http.Request) {
	var ref secretRef
	if err := json.NewDecoder(r.Body).Decode(&ref); err != nil {
		writeErr(w, badRequest("body must be {repo, file, key, value}"))
		return
	}
	s.editSecret(w, r, ref, "secret.write", func(root *yaml.Node) error { return sops.Set(root, ref.Key, ref.Value) })
}

func (s *Server) handleSecretDelete(w http.ResponseWriter, r *http.Request) {
	ref := refFromQuery(r)
	s.editSecret(w, r, ref, "secret.delete", func(root *yaml.Node) error { return sops.Delete(root, ref.Key) })
}

type newSecret struct {
	Repo      int    `json:"repo"`
	File      string `json:"file"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

func (s *Server) handleSecretFile(w http.ResponseWriter, r *http.Request) {
	var req newSecret
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeErr(w, badRequest("body must be {repo, file, name, namespace}"))
		return
	}
	dir, _, err := s.secretFile(secretRef{Repo: req.Repo, File: req.File})
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := repo.NewSecret(dir, req.File, req.Name, req.Namespace); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			err = conflict("no " + sops.ConfigFile + " in the repo; it names the recipients")
		} else {
			err = conflict(err.Error())
		}
		writeErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), "", "secret.create", req.File)
	s.refresh("", "secrets")
	w.WriteHeader(http.StatusCreated)
}
