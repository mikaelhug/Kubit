package api

import (
	"io"
	"net/http"
	"strconv"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/k8s"
)

func (s *Server) k8sRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/clusters/{name}/workloads", s.handleWorkloads)
	r.HandleFunc("GET /api/v1/clusters/{name}/namespaces", s.handleNamespaces)
	r.HandleFunc("GET /api/v1/clusters/{name}/pods", s.handlePods)
	r.HandleFunc("GET /api/v1/clusters/{name}/pods/{namespace}/{pod}/events", s.handlePodEvents)
	r.HandleFunc("GET /api/v1/clusters/{name}/pods/{namespace}/{pod}/logs", s.handlePodLogs)
	r.HandleFunc("GET /api/v1/clusters/{name}/network", s.handleNetwork)
	r.HandleFunc("GET /api/v1/clusters/{name}/storage", s.handleStorage)
	r.HandleFunc("GET /api/v1/clusters/{name}/flux", s.handleFlux)
	r.HandleFunc("GET /api/v1/clusters/{name}/builds", s.handleBuilds)
}

func (s *Server) kube(w http.ResponseWriter, r *http.Request) (*k8s.Client, bool) {
	kc, err := s.manager.KubeClient(r.Context(), r.PathValue("name"))
	if err != nil {
		writeErr(w, err)
		return nil, false
	}
	return kc, true
}

func (s *Server) handleWorkloads(w http.ResponseWriter, r *http.Request) {
	kc, ok := s.kube(w, r)
	if !ok {
		return
	}
	list, err := kc.Workloads(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	if list == nil {
		list = []k8s.Workload{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleFlux(w http.ResponseWriter, r *http.Request) {
	kc, ok := s.kube(w, r)
	if !ok {
		return
	}
	list, err := kc.FluxObjects(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleBuilds(w http.ResponseWriter, r *http.Request) {
	kc, ok := s.kube(w, r)
	if !ok {
		return
	}
	list, err := kc.Builds(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type namespaceRow struct {
	k8s.Namespace
	Platform bool   `json:"platform"`
	Addon    string `json:"addon,omitempty"`
}

func (s *Server) handleNamespaces(w http.ResponseWriter, r *http.Request) {
	kc, ok := s.kube(w, r)
	if !ok {
		return
	}
	list, err := kc.Namespaces(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, namespaceRows(list))
}

func namespaceRows(list []k8s.Namespace) []namespaceRow {
	out := make([]namespaceRow, 0, len(list))
	for _, n := range list {
		addon, platform := cluster.PlatformNamespace(n.Name)
		out = append(out, namespaceRow{Namespace: n, Platform: platform, Addon: addon})
	}
	return out
}

func (s *Server) handlePods(w http.ResponseWriter, r *http.Request) {
	kc, ok := s.kube(w, r)
	if !ok {
		return
	}
	list, err := kc.Pods(r.Context(), r.URL.Query().Get("namespace"), r.URL.Query().Get("selector"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handlePodEvents(w http.ResponseWriter, r *http.Request) {
	kc, ok := s.kube(w, r)
	if !ok {
		return
	}
	list, err := kc.PodEvents(r.Context(), r.PathValue("namespace"), r.PathValue("pod"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handlePodLogs streams text; ?container=, ?tail=, ?follow=true.
func (s *Server) handlePodLogs(w http.ResponseWriter, r *http.Request) {
	kc, ok := s.kube(w, r)
	if !ok {
		return
	}
	tail, _ := strconv.ParseInt(r.URL.Query().Get("tail"), 10, 64)
	if tail <= 0 {
		tail = 500
	}
	rc, err := kc.PodLogs(r.Context(), r.PathValue("namespace"), r.PathValue("pod"), r.URL.Query().Get("container"), tail, r.URL.Query().Get("follow") == "true")
	if err != nil {
		writeErr(w, err)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	fl, _ := w.(http.Flusher)
	buf := make([]byte, 16*1024)
	for {
		n, err := rc.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
			if fl != nil {
				fl.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

type networkView struct {
	Services  []k8s.Service  `json:"services"`
	Ingresses []k8s.Ingress  `json:"ingresses"`
	Pool      *k8s.PoolUsage `json:"pool,omitempty"`
	PoolError string         `json:"poolError,omitempty"`
}

func (s *Server) handleNetwork(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	kc, ok := s.kube(w, r)
	if !ok {
		return
	}
	svcs, err := kc.Services(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	ings, err := kc.Ingresses(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	view := networkView{Services: svcs, Ingresses: ings}
	if c, _, err := s.manager.LoadCluster(r.Context(), name); err == nil && c.Spec.Platform.MetalLB.Enabled {
		if pool, err := k8s.PoolUsageFor(c.Spec.Platform.MetalLB.Range, svcs); err == nil {
			view.Pool = pool
		} else {
			view.PoolError = err.Error()
		}
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleStorage(w http.ResponseWriter, r *http.Request) {
	kc, ok := s.kube(w, r)
	if !ok {
		return
	}
	st, err := kc.Storage(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

var _ = io.EOF
