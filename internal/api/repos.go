package api

import (
	"context"
	"fmt"
	"log"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/repo"
)

type servedRepo struct {
	Dir     string `json:"dir"`
	Name    string `json:"name"`
	Cluster string `json:"cluster,omitempty"`
	Error   string `json:"error,omitempty"`
	subnets []string
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
	s.repos.mu.Lock()
	for _, a := range abs {
		s.repos.repos = append(s.repos.repos, &servedRepo{Dir: a, Name: filepath.Base(a)})
	}
	s.repos.mu.Unlock()
	for _, a := range abs {
		go s.loadRepo(ctx, a)
	}
	return repo.Watch(ctx, abs, func(dir string) {
		s.loadRepo(ctx, dir)
		s.refresh("", "secrets")
	})
}

func (s *Server) loadRepo(ctx context.Context, dir string) {
	entry := s.repoAt(dir)
	if entry == nil {
		return
	}
	update := func(f func(*servedRepo)) {
		s.repos.mu.Lock()
		f(entry)
		s.repos.mu.Unlock()
	}
	if _, err := os.Stat(filepath.Join(dir, repo.ClusterFile)); err != nil {
		update(func(e *servedRepo) { e.Cluster, e.Error, e.subnets = "", "", nil })
		return
	}
	r, err := repo.Load(dir)
	if err != nil {
		log.Printf("repo %s: %v", dir, err)
		update(func(e *servedRepo) { e.Error = err.Error() })
		return
	}
	var subnets []string
	for _, n := range r.Cluster.Spec.Nodes {
		if p, err := netip.ParsePrefix(n.IP + "/24"); err == nil {
			if sn := p.Masked().String(); !slices.Contains(subnets, sn) {
				subnets = append(subnets, sn)
			}
		}
	}
	update(func(e *servedRepo) {
		e.Cluster, e.Name, e.Error, e.subnets = r.Cluster.Metadata.Name, r.Cluster.Metadata.Name, "", subnets
	})
	d, err := cluster.FromRepo(r)
	if err == nil {
		_, err = s.manager.Track(ctx, d)
	}
	if err != nil {
		log.Printf("repo %s: %v", dir, err)
		update(func(e *servedRepo) { e.Error = err.Error() })
	}
	s.refresh("", "secrets")
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
	var out []string
	if v, err := s.store.GetSettings(ctx); err == nil {
		out = append(out, v.DiscoverySubnets...)
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
