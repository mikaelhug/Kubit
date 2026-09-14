package api

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/watch"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	"github.com/siderolabs/talos/pkg/machinery/gendata"
)

// AttachWatcher connects the background watcher to the SSE stream and starts it.
func defaultTalosVersion() string { return gendata.VersionTag }

func (s *Server) AttachWatcher(ctx context.Context, w *watch.Watcher) {
	s.watcher = w
	if v, err := s.store.GetSettings(ctx); err == nil && v.WatchIntervalSec > 0 {
		w.Interval = time.Duration(v.WatchIntervalSec) * time.Second
	}
	w.OnStatus = func(name string, st *cluster.Status) {
		s.hub.publish(Message{Kind: "status", Cluster: name, Status: st})
	}
	w.OnEvent = func(e store.EventRow) { s.hub.publish(Message{Kind: "health", Cluster: e.Cluster, Health: &e}) }
	go w.Run(ctx)
}

func (s *Server) healthRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/clusters/{name}/samples", s.handleSamples)
	r.HandleFunc("GET /api/v1/clusters/{name}/events", s.handleEvents2)
	r.HandleFunc("POST /api/v1/clusters/{name}/events/ack", s.handleAckAll)
	r.HandleFunc("POST /api/v1/events/{id}/ack", s.handleAck)
	r.HandleFunc("GET /api/v1/versions", s.handleVersions)
}

func (s *Server) handleSamples(w http.ResponseWriter, r *http.Request) {
	rng := r.URL.Query().Get("range")
	d := 24 * time.Hour
	switch rng {
	case "1h":
		d = time.Hour
	case "6h":
		d = 6 * time.Hour
	case "7d":
		d = 7 * 24 * time.Hour
	case "30d":
		d = 30 * 24 * time.Hour
	}
	rows, err := s.store.Samples(r.Context(), r.PathValue("name"), r.URL.Query().Get("node"), time.Now().Add(-d))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) handleEvents2(w http.ResponseWriter, r *http.Request) {
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

// Versions lists what an upgrade may target. Talos versions come from the Image
// Factory; Kubernetes minors are the range this build's machinery (Talos
// gendata.VersionTag) supports, which is also the range Kubit validates against.
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
	v := Versions{Machinery: gendata.VersionTag, MinTalos: "v1.14.0", KubernetesLatest: "v" + constants.DefaultKubernetesVersion,
		Note: "Kubernetes compatibility is checked against Talos " + gendata.VersionTag + "'s support window; a Talos release newer than Kubit's machinery may support more."}
	if list, err := s.manager.Factory.Versions(r.Context()); err == nil {
		for _, t := range list {
			if strings.HasPrefix(t, "v1.") && talosAtLeast(t, "v1.14.0") {
				v.Talos = append(v.Talos, t)
			}
		}
		v.TalosSource = s.manager.Factory.BaseURL
	}
	sort.Slice(v.Talos, func(i, j int) bool { return versionLess(v.Talos[j], v.Talos[i]) })
	major, minor := parseMinor(constants.DefaultKubernetesVersion)
	for i := 0; i < constants.SupportedKubernetesVersions; i++ {
		v.KubernetesMinor = append(v.KubernetesMinor, "v"+strconv.Itoa(major)+"."+strconv.Itoa(minor-i))
	}
	writeJSON(w, http.StatusOK, v)
}

func parseMinor(v string) (int, int) {
	parts := strings.SplitN(strings.TrimPrefix(v, "v"), ".", 3)
	if len(parts) < 2 {
		return 0, 0
	}
	a, _ := strconv.Atoi(parts[0])
	b, _ := strconv.Atoi(parts[1])
	return a, b
}

func talosAtLeast(v, min string) bool { return !versionLess(v, min) }

// versionLess orders semver-ish tags; prereleases sort before their release.
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
