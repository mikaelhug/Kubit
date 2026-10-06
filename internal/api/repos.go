package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/repo"
	"github.com/mikael/kubit/internal/store"
)

type servedRepo struct {
	Dir     string `json:"dir"`
	Name    string `json:"name"`
	Cluster string `json:"cluster,omitempty"`
	Error   string `json:"error,omitempty"`
	subnets []string
	nodes   []config.Node
}

type repoSet struct {
	mu    sync.Mutex
	repos []*servedRepo
}

func (s *Server) ServeRepos(ctx context.Context, dirs []string) error {
	var abs []string
	for _, d := range dirs {
		a, err := filepath.Abs(d)
		if err != nil {
			return err
		}
		if st, err := os.Stat(a); err != nil || !st.IsDir() {
			return fmt.Errorf("%s: not a directory", d)
		}
		abs = append(abs, a)
	}
	s.serveCtx = ctx
	for _, a := range abs {
		if _, err := s.serveRepo(a); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) serveRepo(dir string) (*cluster.Desired, error) {
	ctx := s.serveCtx
	s.repos.mu.Lock()
	served := slices.ContainsFunc(s.repos.repos, func(r *servedRepo) bool { return r.Dir == dir })
	if !served {
		s.repos.repos = append(s.repos.repos, &servedRepo{Dir: dir, Name: filepath.Base(dir)})
	}
	s.repos.mu.Unlock()
	d := s.readRepo(dir)
	if d != nil {
		s.manager.Register(d)
		go s.trackRepo(ctx, dir, d, true)
	}
	if served {
		return d, nil
	}
	return d, repo.Watch(ctx, []string{dir}, repo.Events{
		Changed: func(dir string) { s.loadRepo(ctx, dir, true) },
		Secrets: func(string) { s.refresh("", scopeSecrets) },
		Git:     func(dir string) { s.refreshRepo(dir); s.refresh("", scopeSecrets) },
	})
}

const scopeRepo = "repo"

func (s *Server) refreshRepo(dir string) {
	if e := s.repoAt(dir); e != nil && e.Cluster != "" {
		s.refresh(e.Cluster, scopeRepo)
	}
}

type repoView struct {
	Dir string        `json:"dir"`
	Git repo.GitState `json:"git"`
}

func (s *Server) handleRepo(w http.ResponseWriter, r *http.Request) {
	dir, err := s.repoOf(r.PathValue("name"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, repoView{Dir: dir, Git: repo.Status(r.Context(), dir)})
}

func (s *Server) repoOf(name string) (string, error) {
	for _, r := range s.servedRepos() {
		if r.Cluster == name {
			return r.Dir, nil
		}
	}
	return "", fmt.Errorf("cluster %q: %w", name, store.ErrNotFound)
}

func (r servedRepo) declares(m store.Machine) bool {
	return slices.ContainsFunc(r.nodes, func(n config.Node) bool {
		return (n.MAC != "" && strings.EqualFold(n.MAC, m.MAC)) || n.IP == m.IP || n.TargetIP() == m.IP
	})
}

func (s *Server) machineSeen(ctx context.Context, m store.Machine) {
	for _, r := range s.servedRepos() {
		if r.Cluster == "" || !r.declares(m) {
			continue
		}
		if row, err := s.store.GetCluster(r.Cluster); err == nil && cluster.Live(row.State) {
			continue
		}
		if _, busy := s.reloading.LoadOrStore(r.Dir, true); busy {
			continue
		}
		go func(dir string) {
			defer s.reloading.Delete(dir)
			s.loadRepo(ctx, dir, false)
		}(r.Dir)
	}
}

func (s *Server) loadRepo(ctx context.Context, dir string, replan bool) {
	if d := s.readRepo(dir); d != nil {
		s.trackRepo(ctx, dir, d, replan)
	}
}

func (s *Server) trackRepo(ctx context.Context, dir string, d *cluster.Desired, replan bool) {
	if replan {
		s.kickPlan(d.Cluster.Metadata.Name, "repo")
	}
	if _, err := s.manager.Track(ctx, d); err != nil {
		log.Printf("repo %s: %v", dir, err)
		if e := s.repoAt(dir); e != nil {
			s.repos.mu.Lock()
			e.Error = err.Error()
			s.repos.mu.Unlock()
		}
	}
	s.refreshLoaded(d.Cluster.Metadata.Name)
}

func (s *Server) refreshLoaded(name string) {
	s.refresh(name, scopeSnapshots, scopeRepo)
	s.refresh(name, "sops")
	s.refresh("", scopeSecrets, scopeMachines)
}

func (s *Server) readRepo(dir string) *cluster.Desired {
	entry := s.repoAt(dir)
	if entry == nil {
		return nil
	}
	update := func(f func(*servedRepo)) {
		s.repos.mu.Lock()
		f(entry)
		s.repos.mu.Unlock()
	}
	if _, err := os.Stat(filepath.Join(dir, repo.ClusterFile)); err != nil {
		update(func(e *servedRepo) { e.Cluster, e.Error, e.subnets = "", "", nil })
		return nil
	}
	r, err := repo.Load(dir)
	if err != nil {
		log.Printf("repo %s: %v", dir, err)
		update(func(e *servedRepo) { e.Error = err.Error() })
		return nil
	}
	s.store.SetClusterHash(r.Cluster.Metadata.Name, repo.Fingerprint(r.Spec))
	var subnets []string
	for _, n := range r.Cluster.Spec.Nodes {
		if p, ok := config.Slash24(n.IP); ok {
			if sn := p.String(); !slices.Contains(subnets, sn) {
				subnets = append(subnets, sn)
			}
		}
	}
	update(func(e *servedRepo) {
		e.Cluster, e.Name, e.Error, e.subnets, e.nodes = r.Cluster.Metadata.Name, r.Cluster.Metadata.Name, "", subnets, r.Cluster.Spec.Nodes
	})
	d, err := cluster.FromRepo(r)
	if err != nil {
		log.Printf("repo %s: %v", dir, err)
		update(func(e *servedRepo) { e.Error = err.Error() })
		return nil
	}
	return d
}

func (s *Server) repoAt(dir string) *servedRepo {
	s.repos.mu.Lock()
	defer s.repos.mu.Unlock()
	for _, r := range s.repos.repos {
		if r.Dir == dir {
			return r
		}
	}
	return nil
}

func (s *Server) servedRepos() []servedRepo {
	s.repos.mu.Lock()
	defer s.repos.mu.Unlock()
	out := make([]servedRepo, 0, len(s.repos.repos))
	for _, r := range s.repos.repos {
		out = append(out, *r)
	}
	return out
}

func (s *Server) discoverySubnets(ctx context.Context) []string {
	out := slices.Clone(s.settings.DiscoverySubnets)
	if lan, ok := config.Slash24(cluster.DefaultGateway()); ok && !slices.Contains(out, lan.String()) {
		out = append(out, lan.String())
	}
	for _, r := range s.servedRepos() {
		for _, sn := range r.subnets {
			if !slices.Contains(out, sn) {
				out = append(out, sn)
			}
		}
	}
	return out
}
