package api

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mikaelhug/kubit/internal/cluster"
	"github.com/mikaelhug/kubit/internal/config"
	"github.com/mikaelhug/kubit/internal/watch"
	"github.com/siderolabs/talos/pkg/machinery/constants"
)

func (s *Server) Start(ctx context.Context) {
	w := s.watcher
	w.OnStatus = func(name string, st *cluster.Status) {
		s.hub.publish(Message{Kind: "status", Cluster: name, Status: st})
		s.noteStatus(name, st)
		s.checkCertificates(ctx, name)
	}
	w.Alerts.OnEvent = func(e watch.Event) { s.hub.publish(Message{Kind: "health", Cluster: e.Cluster, Health: &e}) }
	w.Alerts.Forward = s.forwardEvent
	w.OnRefresh = func(name, scope string) { s.refresh(name, scope) }
	w.ScanSubnets = s.scanSubnets
	w.OnObserver = func(o watch.ObserverState) {
		if o.Online {
			w.Alerts.Resolve(watch.KubitKey, "", "observer.offline", watch.Event{Cluster: watch.KubitKey, Severity: "info", Kind: "observer.online", Message: "Kubit reaches the network again; alerts resume"})
			return
		}
		w.Alerts.Raise(watch.Event{Cluster: watch.KubitKey, Severity: "warn", Kind: "observer.offline", Message: "Kubit cannot reach the local network (" + o.Error + "); cluster alerts are paused", Notify: true})
	}
	s.attachLive(ctx)
	go s.watchPXE(ctx)
	go s.watchVersions(ctx)
	go s.planDrift(ctx)
	go w.Run(ctx)
}

func (s *Server) watchVersions(ctx context.Context) {
	last := s.latestStableTalos(ctx)
	t := time.NewTicker(versionsTTL)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if v := s.latestStableTalos(ctx); v != last {
			last = v
			s.hub.publish(Message{Kind: "versions"})
		}
	}
}

func (s *Server) healthRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/clusters/{name}/samples", s.handleSamples)
	r.HandleFunc("GET /api/v1/clusters/{name}/events", s.handleClusterEvents)
	r.HandleFunc("GET /api/v1/versions", s.handleVersions)
}

func (s *Server) handleSamples(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.watcher.Samples(r.PathValue("name"), r.URL.Query().Get("node"), sampleRange(r.URL.Query().Get("range"))))
}

func sampleRange(rng string) time.Duration {
	switch rng {
	case "5m":
		return 5 * time.Minute
	case "30m":
		return 30 * time.Minute
	case "1h":
		return time.Hour
	case "6h":
		return 6 * time.Hour
	}
	return 24 * time.Hour
}

func (s *Server) handleClusterEvents(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 200
	}
	writeJSON(w, http.StatusOK, s.watcher.Alerts.List(r.PathValue("name"), limit))
}

type Versions struct {
	Talos            []string `json:"talos"`
	KubernetesLatest string   `json:"kubernetesLatest"`
	MinTalos         string   `json:"minTalos"`
}

func (s *Server) handleVersions(w http.ResponseWriter, r *http.Request) {
	v := Versions{Talos: []string{}, MinTalos: config.MinTalosVersion, KubernetesLatest: "v" + constants.DefaultKubernetesVersion}
	if list, err := s.factoryVersions(r.Context()); err == nil {
		for _, t := range list {
			if strings.HasPrefix(t, "v1.") && talosAtLeast(t, config.MinTalosVersion) {
				v.Talos = append(v.Talos, t)
			}
		}
	}
	sort.Slice(v.Talos, func(i, j int) bool { return versionLess(v.Talos[j], v.Talos[i]) })
	writeJSON(w, http.StatusOK, v)
}

const versionsTTL = time.Hour

func (s *Server) factoryVersions(ctx context.Context) ([]string, error) {
	s.versionsMu.Lock()
	list, at := s.talosList, s.talosListAt
	s.versionsMu.Unlock()
	if list != nil && time.Since(at) < versionsTTL {
		return list, nil
	}
	list, err := s.manager.Factory.Versions(ctx)
	if err != nil {
		return nil, err
	}
	s.versionsMu.Lock()
	s.talosList, s.talosListAt = list, time.Now()
	s.versionsMu.Unlock()
	return list, nil
}

func (s *Server) latestStableTalos(ctx context.Context) string {
	list, _ := s.factoryVersions(ctx)
	latest := ""
	for _, t := range list {
		if strings.HasPrefix(t, "v1.") && splitVer(t).pre == "" && (latest == "" || versionLess(latest, t)) {
			latest = t
		}
	}
	return latest
}

func talosAtLeast(v, min string) bool { return !versionLess(v, min) }

func versionLess(a, b string) bool {
	pa, pb := splitVer(a), splitVer(b)
	for i := 0; i < 3; i++ {
		if pa.n[i] != pb.n[i] {
			return pa.n[i] < pb.n[i]
		}
	}
	if (pa.pre == "") != (pb.pre == "") {
		return pa.pre != ""
	}
	return pa.pre < pb.pre
}

type ver struct {
	n   [3]int
	pre string
}

func splitVer(v string) ver {
	v = strings.TrimPrefix(v, "v")
	var out ver
	if i := strings.IndexByte(v, '-'); i >= 0 {
		out.pre = v[i+1:]
		v = v[:i]
	}
	for i, p := range strings.SplitN(v, ".", 3) {
		out.n[i], _ = strconv.Atoi(p)
	}
	return out
}

func (s *Server) alerts() *watch.Alerts {
	return s.watcher.Alerts
}
