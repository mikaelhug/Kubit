package api

import (
	"net/http"
	"strconv"

	"github.com/mikaelhug/kubit/internal/cluster"
	"github.com/mikaelhug/kubit/internal/k8s"
)

func (s *Server) k8sRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/clusters/{name}/workloads", s.kubeJSON(func(r *http.Request, kc *k8s.Client) (any, error) {
		list, err := kc.Workloads(r.Context())
		if list == nil {
			list = []k8s.Workload{}
		}
		return list, err
	}))
	r.HandleFunc("GET /api/v1/clusters/{name}/namespaces", s.kubeJSON(func(r *http.Request, kc *k8s.Client) (any, error) {
		list, err := kc.Namespaces(r.Context())
		return namespaceRows(list), err
	}))
	r.HandleFunc("GET /api/v1/clusters/{name}/pods", s.kubeJSON(func(r *http.Request, kc *k8s.Client) (any, error) {
		return kc.Pods(r.Context(), r.URL.Query().Get("namespace"), r.URL.Query().Get("selector"))
	}))
	r.HandleFunc("GET /api/v1/clusters/{name}/pods/{namespace}/{pod}/events", s.kubeJSON(func(r *http.Request, kc *k8s.Client) (any, error) {
		return kc.PodEvents(r.Context(), r.PathValue("namespace"), r.PathValue("pod"))
	}))
	r.HandleFunc("GET /api/v1/clusters/{name}/flux", s.kubeJSON(func(r *http.Request, kc *k8s.Client) (any, error) {
		return kc.FluxObjects(r.Context())
	}))
	r.HandleFunc("GET /api/v1/clusters/{name}/builds", s.kubeJSON(func(r *http.Request, kc *k8s.Client) (any, error) {
		return kc.Builds(r.Context())
	}))
	r.HandleFunc("GET /api/v1/clusters/{name}/storage", s.kubeJSON(func(r *http.Request, kc *k8s.Client) (any, error) {
		return kc.Storage(r.Context())
	}))
	r.HandleFunc("GET /api/v1/clusters/{name}/network", s.kubeJSON(s.network))
	r.HandleFunc("GET /api/v1/clusters/{name}/pods/{namespace}/{pod}/logs", s.handlePodLogs)
}

func (s *Server) kubeJSON(fn func(r *http.Request, kc *k8s.Client) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kc, err := s.manager.KubeClient(r.Context(), r.PathValue("name"))
		if err != nil {
			writeErr(w, err)
			return
		}
		v, err := fn(r, kc)
		reply(w, v, err)
	}
}

type namespaceRow struct {
	k8s.Namespace
	Platform bool   `json:"platform"`
	Addon    string `json:"addon,omitempty"`
}

func namespaceRows(list []k8s.Namespace) []namespaceRow {
	out := make([]namespaceRow, 0, len(list))
	for _, n := range list {
		addon, platform := cluster.PlatformNamespace(n.Name)
		out = append(out, namespaceRow{Namespace: n, Platform: platform, Addon: addon})
	}
	return out
}

func (s *Server) handlePodLogs(w http.ResponseWriter, r *http.Request) {
	kc, err := s.manager.KubeClient(r.Context(), r.PathValue("name"))
	if err != nil {
		writeErr(w, err)
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
	write := textStream(w)
	buf := make([]byte, 16*1024)
	for {
		n, err := rc.Read(buf)
		if n > 0 {
			write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

type networkView struct {
	Services  []k8s.Service  `json:"services"`
	Ingresses []k8s.Ingress  `json:"ingresses"`
	Routes    []k8s.Route    `json:"routes"`
	RoutesErr string         `json:"routesError,omitempty"`
	Pool      *k8s.PoolUsage `json:"pool,omitempty"`
	PoolError string         `json:"poolError,omitempty"`
}

func (s *Server) network(r *http.Request, kc *k8s.Client) (any, error) {
	svcs, err := kc.Services(r.Context())
	if err != nil {
		return nil, err
	}
	ings, err := kc.Ingresses(r.Context())
	if err != nil {
		return nil, err
	}
	view := networkView{Services: svcs, Ingresses: ings, Routes: []k8s.Route{}}
	if routes, err := kc.HTTPRoutes(r.Context()); err == nil {
		view.Routes = routes
	} else {
		view.RoutesErr = err.Error()
	}
	if c, _, err := s.manager.LoadCluster(r.PathValue("name")); err == nil && c.Spec.Platform.MetalLB.Enabled {
		if pool, err := k8s.PoolUsageFor(c.Spec.Platform.MetalLB.Range, svcs); err == nil {
			view.Pool = pool
		} else {
			view.PoolError = err.Error()
		}
	}
	return view, nil
}
