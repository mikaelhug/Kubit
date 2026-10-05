package api

import (
	"context"
	"io/fs"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/watch"
	"github.com/mikael/kubit/web"
)

type Server struct {
	mux         *http.ServeMux
	version     string
	manager     *cluster.Manager
	store       *store.Store
	hub         *hub
	started     time.Time
	watcher     *watch.Watcher
	periodic    throttle
	versionsMu  sync.Mutex
	versionsAt  time.Time
	latestTalos string
	talosList   talosList
	pxeMu       sync.Mutex
	pxeLast     pxeSnapshot
	ctx         context.Context
	stop        context.CancelFunc
	stopDaemon  func()
	token       string
	repos       repoSet
}

func New(version string, m *cluster.Manager, token string) *Server {
	s := &Server{mux: http.NewServeMux(), version: version, manager: m, store: m.Store, hub: newHub(), token: token, started: time.Now()}
	s.ctx, s.stop = context.WithCancel(context.Background())
	s.routes()
	return s
}

func (s *Server) Start() {
	if v, err := s.store.GetSettings(s.ctx); err == nil {
		s.manager.Factory.SetBaseURL(v.FactoryURL)
	}
}

func (s *Server) routes() {
	s.liveRoutes()
	s.clusterRoutes()
	s.addonRoutes()
	s.nodeTalosRoutes()
	s.discoverRoutes()
	s.nodeRoutes()
	s.healthRoutes()
	s.k8sRoutes()
	s.pxeRoutes()
	s.etcdRoutes()
	s.certRoutes()
	s.sopsRoutes()
	s.secretRoutes()
	s.mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeErr(w, &statusError{Status: http.StatusNotFound, Msg: "not found"})
	})
	dist, _ := fs.Sub(web.Dist, "dist")
	s.mux.Handle("/", spaHandler(http.FS(dist)))
}

func Loopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
