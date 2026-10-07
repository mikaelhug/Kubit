package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/mikaelhug/kubit/internal/cluster"
	"github.com/mikaelhug/kubit/internal/repo"
	"github.com/mikaelhug/kubit/internal/talos"
)

const scopeDiscovery = "discovery"

type scanState struct {
	mu       sync.Mutex
	running  int
	lastScan time.Time
}

type discoverView struct {
	Subnets    []string  `json:"subnets"`
	Scanning   bool      `json:"scanning"`
	LastScanAt time.Time `json:"lastScanAt,omitzero"`
	Every      int       `json:"everySeconds"`
	NewCluster string    `json:"newCluster,omitempty"`
	Apps       string    `json:"apps,omitempty"`
}

func (s *Server) newClusterDirs() (string, string) {
	if len(s.startDirs) == 0 || exists(filepath.Join(s.startDirs[0], repo.ClusterFile)) {
		return "", ""
	}
	apps := ""
	if len(s.startDirs) > 1 {
		apps = repo.Tilde(s.startDirs[1])
	}
	for _, r := range s.servedRepos() {
		if apps != "" || r.Cluster == "" {
			continue
		}
		if d, err := s.manager.Desired(r.Cluster); err == nil {
			if fr := d.Cluster.Spec.Platform.Flux.Repository; fr != nil && fr.Checkout != "" {
				apps = repo.Tilde(repo.CheckoutDir(d.Dir, fr.Checkout))
			}
		}
	}
	return repo.Tilde(s.startDirs[0]), apps
}

func (s *Server) discoverRoutes() {
	s.mux.HandleFunc("GET /api/v1/discover", func(w http.ResponseWriter, r *http.Request) {
		s.scans.mu.Lock()
		v := discoverView{Scanning: s.scans.running > 0, LastScanAt: s.scans.lastScan}
		s.scans.mu.Unlock()
		v.Subnets = s.discoverySubnets(r.Context())
		v.Every = int(s.watcher.ScanInterval.Seconds())
		v.NewCluster, v.Apps = s.newClusterDirs()
		writeJSON(w, http.StatusOK, v)
	})
	s.mux.HandleFunc("POST /api/v1/discover", s.handleDiscover)
}

type discoverRequest struct {
	Targets []string `json:"targets"`
}

func (s *Server) handleDiscover(w http.ResponseWriter, r *http.Request) {
	var req discoverRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, badRequest(`body must be {"targets": ["cidr or ip", ...]}`))
		return
	}
	if len(req.Targets) == 0 {
		req.Targets = s.discoverySubnets(r.Context())
	}
	if _, err := talos.ExpandTargets(req.Targets); err != nil {
		writeErr(w, invalid(err))
		return
	}
	go s.scan(s.serveCtx, req.Targets, true)
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) scanSubnets(ctx context.Context) {
	if targets := s.discoverySubnets(ctx); len(targets) > 0 {
		s.scan(ctx, targets, false)
	}
}

func (s *Server) scan(ctx context.Context, targets []string, all bool) {
	addrs, err := talos.ExpandTargets(targets)
	if err != nil {
		return
	}
	s.scans.mu.Lock()
	s.scans.running++
	s.scans.mu.Unlock()
	s.refresh("", scopeDiscovery)
	var found []talos.ScanResult
	for _, r := range talos.Scan(ctx, addrs, 64, 2*time.Second) {
		if all || r.State == talos.StateMaintenance {
			found = append(found, r)
		}
	}
	cluster.RecordScan(s.store, found)
	s.scans.mu.Lock()
	s.scans.running--
	s.scans.lastScan = time.Now().UTC()
	s.scans.mu.Unlock()
	s.refresh("", scopeDiscovery)
}
