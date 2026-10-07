package api

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"

	"github.com/mikaelhug/kubit/internal/gitremote"
	"github.com/mikaelhug/kubit/internal/repo"
	"github.com/mikaelhug/kubit/internal/sops"
)

type appsRequest struct {
	Dir          string   `json:"dir"`
	Environment  string   `json:"environment"`
	Environments []string `json:"environments"`
	Path         string   `json:"path"`
}

type appsReview struct {
	Repo      repo.AppsRepo   `json:"repo"`
	URL       string          `json:"url"`
	Branch    string          `json:"branch"`
	Path      string          `json:"path"`
	Files     []repo.AppsFile `json:"files"`
	DeployKey bool            `json:"deployKey"`
}

type appsCheckout struct {
	Dir         string        `json:"dir"`
	Environment string        `json:"environment,omitempty"`
	Git         repo.GitState `json:"git"`
}

type appsStatus struct {
	URL      string        `json:"url,omitempty"`
	Branch   string        `json:"branch,omitempty"`
	Path     string        `json:"path,omitempty"`
	Checkout *appsCheckout `json:"checkout,omitempty"`
}

func (s *Server) appsRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/apps", s.handleInspectApps)
	r.HandleFunc("GET /api/v1/clusters/{name}/apps", s.handleAppsStatus)
	r.HandleFunc("POST /api/v1/clusters/{name}/apps/review", s.handleAppsReview)
	r.HandleFunc("POST /api/v1/clusters/{name}/apps", s.handleConnectApps)
}

func inspectApps(ctx context.Context, dir string) (repo.AppsRepo, error) {
	d, err := expandHome(dir)
	if err != nil {
		return repo.AppsRepo{}, invalid(err)
	}
	a, err := repo.InspectApps(ctx, d)
	if err != nil {
		return a, badRequest(err.Error())
	}
	if a.Branch == "" {
		return a, badRequest(repo.Tilde(d) + " has no branch checked out")
	}
	return a, nil
}

func (s *Server) handleInspectApps(w http.ResponseWriter, r *http.Request) {
	a, err := inspectApps(r.Context(), r.URL.Query().Get("dir"))
	reply(w, a, err)
}

func reviewApps(ctx context.Context, req appsRequest, cluster, fluxRecipient string, hasKey bool) (*appsReview, repo.AppsRepo, repo.AppsConnect, error) {
	a, err := inspectApps(ctx, req.Dir)
	c := repo.AppsConnect{Cluster: cluster, Environment: req.Environment, Environments: req.Environments, Path: req.Path, FluxRecipient: fluxRecipient}
	if err != nil {
		return nil, a, c, err
	}
	files, err := a.Files(c)
	if err != nil {
		return nil, a, c, invalid(err)
	}
	return &appsReview{Repo: a, URL: a.URL, Branch: a.Branch, Path: a.FluxPath(c), Files: files, DeployKey: gitremote.IsSSH(a.URL) && !hasKey}, a, c, nil
}

func (s *Server) clusterApps(ctx context.Context, name string, req appsRequest) (string, *appsReview, repo.AppsRepo, repo.AppsConnect, error) {
	dir, err := s.repoOf(name)
	if err != nil {
		return "", nil, repo.AppsRepo{}, repo.AppsConnect{}, err
	}
	k, err := s.manager.SOPSKey(name)
	if err != nil {
		return "", nil, repo.AppsRepo{}, repo.AppsConnect{}, conflict(err.Error())
	}
	hasKey := false
	if ids, err := sops.Identities(); err == nil {
		if dk, err := repo.DeployKeyOf(dir, ids); err == nil {
			hasKey = dk.Key != nil
		}
	}
	rv, a, c, err := reviewApps(ctx, req, name, k.Recipient, hasKey)
	return dir, rv, a, c, err
}

func (s *Server) handleAppsReview(w http.ResponseWriter, r *http.Request) {
	var req appsRequest
	if !decode(w, r, &req) {
		return
	}
	_, rv, _, _, err := s.clusterApps(r.Context(), r.PathValue("name"), req)
	reply(w, rv, err)
}

func (s *Server) handleConnectApps(w http.ResponseWriter, r *http.Request) {
	var req struct {
		appsRequest
		Hash string `json:"hash"`
	}
	if !decode(w, r, &req) {
		return
	}
	dir, _, a, c, err := s.clusterApps(r.Context(), r.PathValue("name"), req.appsRequest)
	if err != nil {
		writeErr(w, err)
		return
	}
	if b, err := os.ReadFile(filepath.Join(dir, repo.ClusterFile)); err != nil || repo.Fingerprint(b) != req.Hash {
		writeErr(w, editErr(repo.ErrStale))
		return
	}
	if err := s.writeApps(r.Context(), dir, req.Hash, a, c); err != nil {
		writeErr(w, editErr(err))
		return
	}
	writeHash(w, dir)
}

func (s *Server) writeApps(ctx context.Context, dir, hash string, a repo.AppsRepo, c repo.AppsConnect) error {
	if err := repo.Connect(ctx, dir, hash, a, c); err != nil {
		return err
	}
	if _, err := s.serveRepo(a.Dir); err != nil {
		return err
	}
	s.refresh("", scopeSecrets)
	s.refresh(c.Cluster, scopeApps)
	return nil
}

func (s *Server) checkoutOf(url string) string {
	for _, r := range s.servedRepos() {
		if r.Cluster == "" && slices.ContainsFunc(repo.Remotes(context.Background(), r.Dir), func(u string) bool { return repo.SameRemote(u, url) }) {
			return r.Dir
		}
	}
	return ""
}

func (s *Server) handleAppsStatus(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	d, err := s.manager.Desired(name)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := appsStatus{}
	if fr := d.Cluster.Spec.Platform.Flux.Repository; fr != nil {
		out.URL, out.Branch, out.Path = fr.URL, fr.Branch, fr.Path
		if dir := s.checkoutOf(fr.URL); dir != "" {
			co := &appsCheckout{Dir: repo.Tilde(dir), Git: repo.Status(r.Context(), dir)}
			if a, err := repo.InspectApps(r.Context(), dir); err == nil {
				for _, c := range a.Clusters {
					if c.Name == name {
						co.Environment = c.Environment
					}
				}
			}
			out.Checkout = co
		}
	}
	writeJSON(w, http.StatusOK, out)
}
