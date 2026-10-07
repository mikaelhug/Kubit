package api

import (
	"context"
	"io/fs"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/mikaelhug/kubit/internal/cluster"
	"github.com/mikaelhug/kubit/internal/store"
	"github.com/mikaelhug/kubit/internal/watch"
	"github.com/mikaelhug/kubit/web"
)

type Server struct {
	mux         *http.ServeMux
	version     string
	manager     *cluster.Manager
	store       *store.Store
	hub         *hub
	started     time.Time
	watcher     *watch.Watcher
	scans       scanState
	plans       planner
	versionsMu  sync.Mutex
	talosList   []string
	talosListAt time.Time
	pxeMu       sync.Mutex
	pxeLast     pxeSnapshot
	stopDaemon  func()
	token       string
	repos       repoSet
	reloading   sync.Map
	settings    Settings
	certsMu     sync.Mutex
	certsSeen   map[string]time.Time
	serveCtx    context.Context
	runsMu      sync.Mutex
	runs        map[string]*applyRun
}

func New(version string, m *cluster.Manager, token string, settings Settings, w *watch.Watcher, stop func()) *Server {
	s := &Server{mux: http.NewServeMux(), version: version, manager: m, store: m.Store, hub: newHub(), token: token, settings: settings, watcher: w, stopDaemon: stop, certsSeen: map[string]time.Time{}, runs: map[string]*applyRun{}, serveCtx: context.Background(), started: time.Now()}
	m.Factory.SetBaseURL(settings.FactoryURL)
	m.Checkout = s.checkoutOf
	s.routes()
	return s
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
	s.editRoutes()
	s.planRoutes()
	s.specRoutes()
	s.appsRoutes()
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
