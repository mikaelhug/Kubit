package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/repo"
	"github.com/mikael/kubit/internal/sops"
	"go.yaml.in/yaml/v4"
)

func (s *Server) secretRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/secrets", s.handleSecrets)
	r.HandleFunc("GET /api/v1/clusters/{name}/secrets", s.handleClusterSecrets)
	r.HandleFunc("GET /api/v1/secrets/values", s.handleSecretValues)
	r.HandleFunc("PATCH /api/v1/secrets/file", s.handleSecretPatch)
	r.HandleFunc("DELETE /api/v1/secrets/file", s.handleSecretDelete)
	r.HandleFunc("POST /api/v1/secrets/move", s.handleSecretMove)
	r.HandleFunc("POST /api/v1/secrets/files", s.handleSecretFile)
}

type secretFileView struct {
	repo.SecretFile
	Repo      int    `json:"repo"`
	Git       string `json:"git,omitempty"`
	Modified  string `json:"modified,omitempty"`
	Cluster   string `json:"cluster,omitempty"`
	FluxReads bool   `json:"fluxReads"`
	Skipped   string `json:"skipped,omitempty"`
}

type secretRepo struct {
	servedRepo
	Index         int              `json:"index"`
	Files         []secretFileView `json:"files"`
	FluxOf        []string         `json:"fluxOf"`
	FluxRecipient string           `json:"fluxRecipient,omitempty"`
	FluxReads     bool             `json:"fluxReads"`
}

type secretIndex struct {
	Repos  []secretRepo      `json:"repos"`
	Labels map[string]string `json:"labels"`
}

type fluxSource struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url,omitempty"`
	Branch  string `json:"branch,omitempty"`
	Path    string `json:"path,omitempty"`
}

type clusterFlux struct {
	source    fluxSource
	recipient string
	dir       string
}

func (s *Server) fluxOf(name, dir string) clusterFlux {
	cf := clusterFlux{dir: dir}
	if k, err := s.manager.SOPSKey(name); err == nil {
		cf.recipient = k.Recipient
	}
	d, err := s.manager.Desired(name)
	if err != nil {
		return cf
	}
	f := d.Cluster.Spec.Platform.Flux
	cf.source.Enabled = f.Enabled
	if f.Repository != nil {
		cf.source.URL, cf.source.Branch, cf.source.Path = f.Repository.URL, f.Repository.Branch, repo.FluxRoot(f.Repository.Path)
	}
	return cf
}

func (s *Server) secretIndex(ctx context.Context) secretIndex {
	served := s.servedRepos()
	idx := secretIndex{Repos: []secretRepo{}, Labels: map[string]string{}}
	if ids, err := sops.Identities(); err == nil {
		for _, id := range ids {
			if r := sops.Recipient(id); r != "" {
				idx.Labels[r] = "you"
			}
		}
	}
	fluxes := map[string]clusterFlux{}
	for _, r := range served {
		if r.Cluster == "" {
			continue
		}
		cf := s.fluxOf(r.Cluster, r.Dir)
		fluxes[r.Cluster] = cf
		if cf.recipient != "" && idx.Labels[cf.recipient] == "" {
			idx.Labels[cf.recipient] = "Flux (" + r.Cluster + ")"
		}
	}
	for i, r := range served {
		files, err := repo.SecretFiles(r.Dir)
		sr := secretRepo{servedRepo: r, Index: i, Files: []secretFileView{}, FluxOf: []string{}}
		if err != nil && sr.Error == "" {
			sr.Error = err.Error()
		}
		if cf, ok := fluxes[r.Cluster]; ok && cf.recipient != "" {
			sr.FluxRecipient, sr.FluxReads = cf.recipient, repo.FluxReads(r.Dir, "", cf.recipient)
		}
		remotes := repo.Remotes(ctx, r.Dir)
		for name, cf := range fluxes {
			if cf.source.Enabled && cf.source.URL != "" && slices.ContainsFunc(remotes, func(u string) bool { return repo.SameRemote(u, cf.source.URL) }) {
				sr.FluxOf = append(sr.FluxOf, name)
			}
		}
		slices.Sort(sr.FluxOf)
		git := repo.FileStatus(ctx, r.Dir)
		for _, f := range files {
			v := secretFileView{SecretFile: f, Repo: i, Git: git[f.Path]}
			if st, err := os.Stat(filepath.Join(r.Dir, filepath.FromSlash(f.Path))); err == nil {
				v.Modified = st.ModTime().UTC().Format(time.RFC3339)
			}
			s.attribute(&v, r, sr.FluxOf, fluxes)
			sr.Files = append(sr.Files, v)
		}
		idx.Repos = append(idx.Repos, sr)
	}
	return idx
}

func (s *Server) attribute(v *secretFileView, r servedRepo, fluxOf []string, fluxes map[string]clusterFlux) {
	for _, name := range fluxOf {
		cf := fluxes[name]
		root := cf.source.Path
		if root != "" && !strings.HasPrefix(v.Path, root+"/") {
			continue
		}
		v.Cluster, v.Skipped = name, repo.Applied(r.Dir, root, v.Path)
		v.FluxReads = cf.recipient != "" && slices.Contains(v.Recipients, cf.recipient)
		return
	}
	if r.Cluster == "" {
		return
	}
	cf := fluxes[r.Cluster]
	v.Cluster = r.Cluster
	v.FluxReads = cf.recipient != "" && slices.Contains(v.Recipients, cf.recipient)
	switch {
	case !cf.source.Enabled:
		v.Skipped = "Flux is off"
	case cf.source.URL == "":
		v.Skipped = "Flux has no repository"
	case slices.Contains(fluxOf, r.Cluster):
		v.Skipped = "outside " + cf.source.Path
	default:
		v.Skipped = "not in Flux's repository"
	}
}

func (s *Server) handleSecrets(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.secretIndex(r.Context()))
}

type sourceView struct {
	Repo    int    `json:"repo"`
	Name    string `json:"name"`
	Dir     string `json:"dir"`
	Cluster bool   `json:"cluster"`
	Flux    bool   `json:"flux"`
	Reads   bool   `json:"reads"`
	Error   string `json:"error,omitempty"`
}

type clusterSecrets struct {
	Flux      fluxSource        `json:"flux"`
	Recipient string            `json:"recipient,omitempty"`
	Sources   []sourceView      `json:"sources"`
	Files     []secretFileView  `json:"files"`
	Labels    map[string]string `json:"labels"`
}

func (s *Server) handleClusterSecrets(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	dir, err := s.repoOf(name)
	if err != nil {
		writeErr(w, err)
		return
	}
	cf := s.fluxOf(name, dir)
	idx := s.secretIndex(r.Context())
	out := clusterSecrets{Flux: cf.source, Recipient: cf.recipient, Sources: []sourceView{}, Files: []secretFileView{}, Labels: idx.Labels}
	for _, sr := range idx.Repos {
		own, flux := sr.Cluster == name, slices.Contains(sr.FluxOf, name)
		if !own && !flux {
			continue
		}
		root := ""
		if flux {
			root = cf.source.Path
		}
		reads := cf.recipient != "" && repo.FluxReads(sr.Dir, root, cf.recipient)
		out.Sources = append(out.Sources, sourceView{Repo: sr.Index, Name: sr.Name, Dir: sr.Dir, Cluster: own, Flux: flux, Reads: reads, Error: sr.Error})
		for _, f := range sr.Files {
			if f.Cluster == name {
				out.Files = append(out.Files, f)
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

type secretRef struct {
	Repo int    `json:"repo"`
	File string `json:"file"`
	Hash string `json:"hash"`
}

func (s *Server) repoDir(i int) (string, error) {
	repos := s.servedRepos()
	if i < 0 || i >= len(repos) {
		return "", badRequest("unknown repo")
	}
	return repos[i].Dir, nil
}

func (s *Server) secretFile(ref secretRef) (string, string, error) {
	dir, err := s.repoDir(ref.Repo)
	if err != nil {
		return "", "", err
	}
	p, err := repo.SecretPath(dir, ref.File)
	if err != nil {
		return "", "", badRequest(err.Error())
	}
	return dir, p, nil
}

func refFromQuery(r *http.Request) secretRef {
	q := r.URL.Query()
	i, err := strconv.Atoi(q.Get("repo"))
	if err != nil {
		i = -1
	}
	return secretRef{Repo: i, File: q.Get("file"), Hash: q.Get("hash")}
}

func editErr(err error) error {
	var se *statusError
	switch {
	case errors.As(err, &se):
		return err
	case errors.Is(err, repo.ErrStale):
		return conflict("The file changed on disk since you opened it.")
	case errors.Is(err, os.ErrNotExist):
		return conflict(err.Error())
	default:
		return badRequest(err.Error())
	}
}

func (s *Server) secretsChanged(dir string) {
	s.refresh("", scopeSecrets)
	s.refreshRepo(dir)
}

const scopeSecrets = "secrets"

func (s *Server) handleSecretValues(w http.ResponseWriter, r *http.Request) {
	_, p, err := s.secretFile(refFromQuery(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	b, err := os.ReadFile(p)
	if err != nil {
		writeErr(w, editErr(err))
		return
	}
	ids, err := sops.Identities()
	if err != nil {
		writeErr(w, err)
		return
	}
	values, err := sops.Values(b, ids)
	if err != nil {
		writeErr(w, conflict(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Hash   string       `json:"hash"`
		Values []sops.Entry `json:"values"`
	}{repo.Fingerprint(b), values})
}

type secretPatch struct {
	secretRef
	Set    []sops.Entry `json:"set"`
	Remove [][]string   `json:"remove"`
}

func validPath(p []string) bool {
	return len(p) > 0 && !slices.ContainsFunc(p, func(k string) bool { return strings.TrimSpace(k) == "" })
}

func (s *Server) handleSecretPatch(w http.ResponseWriter, r *http.Request) {
	var req secretPatch
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, badRequest("body must be {repo, file, hash, set, remove}"))
		return
	}
	dir, p, err := s.secretFile(req.secretRef)
	if err != nil {
		writeErr(w, err)
		return
	}
	if req.Hash == "" {
		writeErr(w, badRequest("hash is required"))
		return
	}
	for _, e := range req.Set {
		if !validPath(e.Path) {
			writeErr(w, badRequest("every key needs a name"))
			return
		}
	}
	for _, k := range req.Remove {
		if !validPath(k) {
			writeErr(w, badRequest("every key needs a name"))
			return
		}
	}
	ids, err := sops.Identities()
	if err != nil {
		writeErr(w, err)
		return
	}
	err = repo.EditSecret(p, req.Hash, ids, func(root *yaml.Node) error {
		for _, k := range req.Remove {
			if err := sops.Delete(root, k); err != nil {
				return err
			}
		}
		for _, e := range req.Set {
			if err := sops.Set(root, e.Path, e.Value); err != nil {
				return err
			}
		}
		return nil
	})
	s.secretsChanged(dir)
	if err != nil {
		writeErr(w, editErr(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSecretDelete(w http.ResponseWriter, r *http.Request) {
	ref := refFromQuery(r)
	dir, _, err := s.secretFile(ref)
	if err != nil {
		writeErr(w, err)
		return
	}
	if ref.Hash == "" {
		writeErr(w, badRequest("hash is required"))
		return
	}
	k, err := repo.DeleteSecret(dir, ref.File, ref.Hash)
	s.secretsChanged(dir)
	if err != nil {
		writeErr(w, editErr(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string][]string{"kustomizations": nonEmpty(k)})
}

func nonEmpty(xs ...string) []string {
	return slices.DeleteFunc(xs, func(x string) bool { return x == "" })
}

type secretMove struct {
	secretRef
	To string `json:"to"`
}

func (s *Server) handleSecretMove(w http.ResponseWriter, r *http.Request) {
	var req secretMove
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.To == "" || req.Hash == "" {
		writeErr(w, badRequest("body must be {repo, file, hash, to}"))
		return
	}
	dir, _, err := s.secretFile(req.secretRef)
	if err != nil {
		writeErr(w, err)
		return
	}
	ids, err := sops.Identities()
	if err != nil {
		writeErr(w, err)
		return
	}
	touched, err := repo.MoveSecret(dir, req.File, path.Clean(req.To), req.Hash, ids)
	s.secretsChanged(dir)
	if err != nil {
		writeErr(w, editErr(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string][]string{"kustomizations": nonEmpty(touched...)})
}

type newSecret struct {
	Repo int    `json:"repo"`
	File string `json:"file"`
	repo.SecretSpec
}

func (s *Server) handleSecretFile(w http.ResponseWriter, r *http.Request) {
	var req newSecret
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeErr(w, badRequest("body must be {repo, file, name, namespace, type, stringData}"))
		return
	}
	dir, _, err := s.secretFile(secretRef{Repo: req.Repo, File: req.File})
	if err != nil {
		writeErr(w, err)
		return
	}
	k, err := repo.NewSecret(dir, req.File, req.SecretSpec)
	s.secretsChanged(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			err = conflict("no " + sops.ConfigFile + " in the repo; it names the recipients")
		}
		writeErr(w, editErr(err))
		return
	}
	writeJSON(w, http.StatusCreated, map[string][]string{"kustomizations": nonEmpty(k)})
}
