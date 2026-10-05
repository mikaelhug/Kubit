package api

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/watch"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	"github.com/siderolabs/talos/pkg/machinery/gendata"
	utilversion "k8s.io/apimachinery/pkg/util/version"
)

func (s *Server) AttachWatcher(ctx context.Context, w *watch.Watcher) {
	s.watcher = w
	w.OnStatus = func(name string, st *cluster.Status) {
		s.hub.publish(Message{Kind: "status", Cluster: name, Status: st})
		s.maybeScheduleSnapshot(ctx, name, st)
		s.periodic.every(name, time.Hour, func() { s.checkCertificates(ctx, name) })
	}
	w.OnEvent = func(e store.EventRow) {
		s.hub.publish(Message{Kind: "health", Cluster: e.Cluster, Health: &e})
		s.forwardEvent(e)
	}
	w.OnRefresh = func(name, scope string) { s.refresh(name, scope) }
	w.Subnets = s.discoverySubnets
	w.OnObserver = func(o watch.ObserverState) {
		s.hub.publish(Message{Kind: "observer", Observer: &o})
		if o.Online {
			if s.store.HasOpenEvent(ctx, store.KubitKey, "", "observer.offline") {
				_ = s.store.ResolveEvents(ctx, store.KubitKey, "", "observer.offline")
				s.raiseEvent(ctx, store.EventRow{Cluster: store.KubitKey, Severity: "info", Kind: "observer.online", Message: "Kubit can reach the network again; alerts resume"})
			}
			return
		}
		if !s.store.HasOpenEvent(ctx, store.KubitKey, "", "observer.offline") {
			s.raiseEvent(ctx, store.EventRow{Cluster: store.KubitKey, Severity: "warn", Kind: "observer.offline", Message: "Kubit cannot reach the local network (" + o.Error + "); cluster alerts are paused"})
		}
	}
	s.attachLive(ctx)
	go s.watchPXE(ctx)
	go s.watchVersions(ctx)
	go w.Run(ctx)
}

func (s *Server) watchVersions(ctx context.Context) {
	last := s.latestStableTalos(ctx)
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.versionsMu.Lock()
		s.versionsAt = time.Time{}
		s.talosList = talosList{}
		s.versionsMu.Unlock()
		if v := s.latestStableTalos(ctx); v != last {
			last = v
			s.hub.publish(Message{Kind: "versions", Key: v})
		}
	}
}

func (s *Server) handleObserver(w http.ResponseWriter, _ *http.Request) {
	if s.watcher == nil {
		writeJSON(w, http.StatusOK, watch.ObserverState{Online: true})
		return
	}
	writeJSON(w, http.StatusOK, s.watcher.Observer())
}

func (s *Server) healthRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/observer", s.handleObserver)
	r.HandleFunc("GET /api/v1/clusters/{name}/samples", s.handleSamples)
	r.HandleFunc("GET /api/v1/clusters/{name}/service-health", s.handleServiceHealth)
	r.HandleFunc("GET /api/v1/clusters/{name}/events", s.handleClusterEvents)
	r.HandleFunc("POST /api/v1/clusters/{name}/events/ack", s.handleAckAll)
	r.HandleFunc("POST /api/v1/events/{id}/ack", s.handleAck)
	r.HandleFunc("GET /api/v1/versions", s.handleVersions)
}

func (s *Server) handleServiceHealth(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var latest *cluster.ServiceHealth
	if s.watcher != nil {
		latest = s.watcher.LatestServices(name)
	}
	alerts, err := s.store.OpenWorkloadEvents(r.Context(), name, 500)
	if err != nil {
		alerts = []store.EventRow{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"latest": latest, "alerts": alerts})
}

func (s *Server) handleSamples(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.Samples(r.Context(), r.PathValue("name"), r.URL.Query().Get("node"), time.Now().Add(-sampleRange(r.URL.Query().Get("range"))))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func sampleRange(rng string) time.Duration {
	switch rng {
	case "1h":
		return time.Hour
	case "6h":
		return 6 * time.Hour
	case "7d":
		return 7 * 24 * time.Hour
	case "30d":
		return 30 * 24 * time.Hour
	}
	return 24 * time.Hour
}

func (s *Server) handleClusterEvents(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.store.Events(r.Context(), r.PathValue("name"), limit, r.URL.Query().Get("unacked") == "true")
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) handleAck(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.store.AckEvent(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAckAll(w http.ResponseWriter, r *http.Request) {
	if err := s.store.AckClusterEvents(r.Context(), r.PathValue("name")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type Versions struct {
	Talos            []string `json:"talos"`
	TalosSource      string   `json:"talosSource"`
	KubernetesMinor  []string `json:"kubernetesMinors"`
	KubernetesLatest string   `json:"kubernetesLatest"`
	Machinery        string   `json:"machinery"`
	MinTalos         string   `json:"minTalos"`
	Note             string   `json:"note"`
}

func (s *Server) handleVersions(w http.ResponseWriter, r *http.Request) {
	v := Versions{Machinery: gendata.VersionTag, MinTalos: config.MinTalosVersion, KubernetesLatest: "v" + constants.DefaultKubernetesVersion,
		Note: "Kubernetes compatibility is checked against Talos " + gendata.VersionTag + "'s support window; a Talos release newer than Kubit's machinery may support more."}
	if list, source, err := s.factoryVersions(r.Context()); err == nil {
		for _, t := range list {
			if strings.HasPrefix(t, "v1.") && talosAtLeast(t, config.MinTalosVersion) {
				v.Talos = append(v.Talos, t)
			}
		}
		v.TalosSource = source
	}
	sort.Slice(v.Talos, func(i, j int) bool { return versionLess(v.Talos[j], v.Talos[i]) })
	if kv, err := utilversion.ParseMajorMinor(constants.DefaultKubernetesVersion); err == nil {
		for i := 0; i < constants.SupportedKubernetesVersions; i++ {
			v.KubernetesMinor = append(v.KubernetesMinor, "v"+strconv.Itoa(int(kv.Major()))+"."+strconv.Itoa(int(kv.Minor())-i))
		}
	}
	writeJSON(w, http.StatusOK, v)
}

type talosList struct {
	source string
	list   []string
	at     time.Time
}

func (s *Server) factoryVersions(ctx context.Context) ([]string, string, error) {
	source := s.manager.Factory.BaseURL()
	s.versionsMu.Lock()
	cached := s.talosList
	s.versionsMu.Unlock()
	if cached.source == source && time.Since(cached.at) < time.Hour {
		return cached.list, source, nil
	}
	list, err := s.manager.Factory.Versions(ctx)
	if err != nil {
		return nil, "", err
	}
	s.versionsMu.Lock()
	s.talosList = talosList{source: source, list: list, at: time.Now()}
	s.versionsMu.Unlock()
	return list, source, nil
}

func (s *Server) latestStableTalos(ctx context.Context) string {
	s.versionsMu.Lock()
	if time.Since(s.versionsAt) < time.Hour {
		latest := s.latestTalos
		s.versionsMu.Unlock()
		return latest
	}
	s.versionsAt = time.Now()
	s.versionsMu.Unlock()
	latest := ""
	if list, err := s.manager.Factory.Versions(ctx); err == nil {
		for _, t := range list {
			if strings.HasPrefix(t, "v1.") && splitVer(t).pre == "" && (latest == "" || versionLess(latest, t)) {
				latest = t
			}
		}
	}
	s.versionsMu.Lock()
	s.latestTalos = latest
	s.versionsMu.Unlock()
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
