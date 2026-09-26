package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mikael/kubit/internal/oob"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/labhost"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
	"github.com/mikael/kubit/internal/tofu"
	"github.com/mikael/kubit/internal/watch"
	"github.com/mikael/kubit/web"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

type Server struct {
	mux         *http.ServeMux
	version     string
	manager     *cluster.Manager
	store       *store.Store
	hub         *hub
	locks       clusterLocks
	cancels     sync.Map
	started     time.Time
	watcher     *watch.Watcher
	certCheck   throttle
	versionsMu  sync.Mutex
	versionsAt  time.Time
	latestTalos string
	crypto      *store.Crypto
	ctx         context.Context
	stop        context.CancelFunc
	token       string
}

func New(version string, m *cluster.Manager, token string, crypto *store.Crypto) *Server {
	s := &Server{mux: http.NewServeMux(), version: version, manager: m, store: m.Store, hub: newHub(), token: token, crypto: crypto, started: time.Now()}
	s.ctx, s.stop = context.WithCancel(context.Background())
	if v, err := m.Store.GetSettings(s.ctx); err == nil {
		m.Factory.SetBaseURL(v.FactoryURL)
	}
	if err := s.store.MarkStaleOperations(s.ctx); err != nil {
		log.Printf("mark interrupted operations: %v", err)
	}
	s.reconcileLabHosts(s.ctx)
	r := s.mux
	r.HandleFunc("GET /api/v1/version", s.handleVersion)
	r.HandleFunc("GET /api/v1/ws", s.handleLive)
	r.HandleFunc("GET /api/v1/operations", s.handleOperations)
	r.HandleFunc("GET /api/v1/operations/{id}", s.handleOperation)
	r.HandleFunc("DELETE /api/v1/operations/{id}", s.handleOperationCancel)
	r.HandleFunc("POST /api/v1/operations/{id}/retry", s.handleOperationRetry)
	r.HandleFunc("GET /api/v1/clusters", s.handleClusters)
	r.HandleFunc("POST /api/v1/clusters", s.handleClusterCreate)
	r.HandleFunc("GET /api/v1/clusters/{name}", s.handleCluster)
	r.HandleFunc("DELETE /api/v1/clusters/{name}", s.handleClusterForget)
	r.HandleFunc("GET /api/v1/clusters/{name}/status", s.handleClusterStatus)
	r.HandleFunc("GET /api/v1/clusters/{name}/yaml", s.handleClusterYAML)
	r.HandleFunc("PUT /api/v1/clusters/{name}/yaml", s.handleClusterYAMLSave)
	r.HandleFunc("PUT /api/v1/clusters/{name}/form", s.handleClusterForm)
	r.HandleFunc("GET /api/v1/clusters/{name}/kubeconfig", s.handleClusterKubeconfig)
	r.HandleFunc("POST /api/v1/clusters/{name}/apply", s.disruptive(s.handleClusterApply))
	r.HandleFunc("GET /api/v1/clusters/{name}/addons", s.handleAddons)
	r.HandleFunc("PUT /api/v1/clusters/{name}/addons/{addon}", s.handleAddonUpdate)
	r.HandleFunc("POST /api/v1/clusters/{name}/platform/plan", s.handlePlatformPlan)
	r.HandleFunc("POST /api/v1/clusters/{name}/platform/apply", s.handlePlatformApply)
	r.HandleFunc("POST /api/v1/clusters/{name}/platform/apply/{planId}", s.handlePlatformApplyPlan)
	r.HandleFunc("POST /api/v1/clusters/{name}/upgrade/talos", s.disruptive(s.handleUpgradeTalos))
	r.HandleFunc("GET /api/v1/clusters/{name}/image", s.handleImageStatus)
	r.HandleFunc("POST /api/v1/clusters/{name}/upgrade/kubernetes", s.disruptive(s.handleUpgradeKubernetes))
	r.HandleFunc("POST /api/v1/clusters/{name}/export", s.handleExport)
	r.HandleFunc("POST /api/v1/clusters/{name}/nodes", s.handleNodeAdd)
	r.HandleFunc("DELETE /api/v1/clusters/{name}/nodes/{hostname}", s.disruptive(s.handleNodeRemove))
	r.HandleFunc("GET /api/v1/nodes", s.handleNodes)
	r.HandleFunc("POST /api/v1/discover", s.handleDiscover)
	r.HandleFunc("GET /api/v1/nodes/{ip}/logs", s.handleNodeLogs)
	r.HandleFunc("GET /api/v1/nodes/{ip}/services", s.handleNodeServices)
	r.HandleFunc("POST /api/v1/nodes/{ip}/reboot", s.handleNodeReboot)
	r.HandleFunc("POST /api/v1/config/validate", s.handleConfigValidate)
	r.HandleFunc("POST /api/v1/config/draft", s.handleConfigDraft)
	s.nodeRoutes()
	s.healthRoutes()
	s.k8sRoutes()
	s.settingsRoutes()
	s.machineRoutes()
	s.etcdRoutes()
	s.offsiteRoutes()
	s.oobRoutes()
	s.labhostRoutes()
	s.labMaintRoutes()
	s.certRoutes()
	s.sopsRoutes()
	s.authRoutes()
	s.mux.HandleFunc("GET /api/v1/clusters/{name}/maintenance", s.handleMaintenance)
	dist, _ := fs.Sub(web.Dist, "dist")
	r.Handle("/", spaHandler(http.FS(dist)))
	return s
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

func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	h := s.hello()
	writeJSON(w, http.StatusOK, map[string]any{"kubit": h.Version, "startedAt": h.StartedAt, "service": h.Service, "pid": h.PID, "os": h.OS})
}

func (s *Server) hello() Hello {
	return Hello{Version: s.version, StartedAt: s.started.UTC().Format(time.RFC3339), Service: os.Getenv("KUBIT_SERVICE") != "", PID: os.Getpid(), OS: runtime.GOOS}
}

func (s *Server) handleOperations(w http.ResponseWriter, r *http.Request) {
	ops, err := s.store.ListOperations(r.Context(), 100)
	if err != nil {
		writeErr(w, err)
		return
	}
	if ops == nil {
		ops = []store.OperationRow{}
	}
	writeJSON(w, http.StatusOK, ops)
}

func (s *Server) handleOperation(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	op, err := s.store.GetOperation(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, op)
}

func (s *Server) handleOperationCancel(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if !s.cancelOperation(id) {
		http.Error(w, "operation is not running", http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleOperationRetry(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	op, err := s.store.GetOperation(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if op.Status == "running" {
		http.Error(w, "operation is still running", http.StatusConflict)
		return
	}
	var newID int64
	switch op.Kind {
	case "cluster.create":
		var req createRequest
		_ = json.Unmarshal(op.Request, &req)
		c, err := config.Parse([]byte(req.YAML))
		if err != nil {
			writeErr(w, err)
			return
		}
		newID, err = s.startCreate(c, req.SkipPlatform, req)
	case "node.add":
		var n config.Node
		_ = json.Unmarshal(op.Request, &n)
		newID, err = s.startNodeAdd(op.Cluster, n)
	case "platform.apply", "platform.plan":
		newID, err = s.runOperation(op.Cluster, op.Kind, nil, func(ctx context.Context, sink cluster.Sink) (any, error) {
			if op.Kind == "platform.plan" {
				return s.manager.PlanPlatform(ctx, op.Cluster, sink)
			}
			return nil, s.manager.ApplyPlatform(ctx, op.Cluster, sink)
		})
	case "discover":
		var req discoverRequest
		_ = json.Unmarshal(op.Request, &req)
		newID, err = s.startDiscover(req.Targets)
	default:
		http.Error(w, "this kind of operation cannot be retried; start it again from its page", http.StatusBadRequest)
		return
	}
	accepted(w, newID, err)
}

type clusterSummary struct {
	store.ClusterRow
	Spec *config.Cluster `json:"spec"`
}

func (s *Server) handleClusters(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.ListClusters(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	out := []clusterSummary{}
	for _, row := range rows {
		c, _ := config.Parse(row.Spec)
		out = append(out, clusterSummary{ClusterRow: row, Spec: c})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCluster(w http.ResponseWriter, r *http.Request) {
	row, err := s.store.GetCluster(r.Context(), r.PathValue("name"))
	if err != nil {
		writeErr(w, err)
		return
	}
	c, _ := config.Parse(row.Spec)
	writeJSON(w, http.StatusOK, clusterSummary{ClusterRow: *row, Spec: c})
}

func (s *Server) handleClusterStatus(w http.ResponseWriter, r *http.Request) {
	if s.watcher != nil && r.URL.Query().Get("fresh") != "true" {
		if st := s.watcher.Latest(r.PathValue("name")); st != nil {
			writeJSON(w, http.StatusOK, st)
			return
		}
	}
	st, err := s.manager.Status(r.Context(), r.PathValue("name"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleClusterYAML(w http.ResponseWriter, r *http.Request) {
	row, err := s.store.GetCluster(r.Context(), r.PathValue("name"))
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	w.Write(row.Spec)
}

func (s *Server) handleClusterYAMLSave(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, err)
		return
	}
	updated, err := config.Parse(body)
	if err != nil {
		writeErr(w, unprocessable(err))
		return
	}
	c, ok := s.editCluster(w, r, "cluster.yaml.save", "", func(c *config.Cluster) error {
		if err := adoptDeclaration(c, updated); err != nil {
			return err
		}
		*c = *updated
		return nil
	})
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"yaml": mustYAML(c)})
}

func (s *Server) handleClusterKubeconfig(w http.ResponseWriter, r *http.Request) {
	sec, err := s.store.GetClusterSecrets(r.Context(), r.PathValue("name"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if sec.Kubeconfig == nil {
		http.Error(w, "no kubeconfig yet", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	w.Header().Set("Content-Disposition", `attachment; filename="kubeconfig"`)
	w.Write(sec.Kubeconfig)
}

type createRequest struct {
	YAML         string `json:"yaml"`
	SkipPlatform bool   `json:"skipPlatform"`
}

func (s *Server) handleClusterCreate(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	c, err := config.Parse([]byte(req.YAML))
	if err != nil {
		writeErr(w, err)
		return
	}
	id, err := s.startCreate(c, req.SkipPlatform, req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"operationId": id, "cluster": c.Metadata.Name})
}

func (s *Server) startCreate(c *config.Cluster, skipPlatform bool, request any) (int64, error) {
	return s.runOperation(c.Metadata.Name, "cluster.create", request, func(ctx context.Context, sink cluster.Sink) (any, error) {
		if err := s.manager.Create(ctx, c, sink); err != nil {
			return nil, err
		}
		if skipPlatform {
			return nil, nil
		}
		return nil, s.manager.ApplyPlatform(ctx, c.Metadata.Name, sink)
	})
}

func (s *Server) handleClusterForget(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	unlock, ok := s.holdLock(w, r, name, clusterBusy)
	if !ok {
		return
	}
	defer unlock()
	if _, err := s.store.GetCluster(r.Context(), name); err != nil {
		writeErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), name, "cluster.forget", "")
	if err := s.store.DeleteCluster(r.Context(), name); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleClusterApply(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req struct {
		YAML string `json:"yaml"`
	}
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	s.startOp(w, name, "cluster.apply", req, func(ctx context.Context, sink cluster.Sink) (any, error) {
		c, _, err := s.manager.LoadCluster(ctx, name)
		if err != nil {
			return nil, err
		}
		if req.YAML != "" {
			updated, err := config.Parse([]byte(req.YAML))
			if err != nil {
				return nil, err
			}
			if err := adoptDeclaration(c, updated); err != nil {
				return nil, err
			}
			if err := s.manager.SaveCluster(ctx, updated, ""); err != nil {
				return nil, err
			}
			c = updated
		}
		return nil, s.manager.ApplyConfigs(ctx, c, "", sink)
	})
}

func (s *Server) handleAddons(w http.ResponseWriter, r *http.Request) {
	list, err := s.manager.Addons(r.Context(), r.PathValue("name"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handlePlatformPlan(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.startOp(w, name, "platform.plan", nil, func(ctx context.Context, sink cluster.Sink) (any, error) {
		diff, err := s.manager.PlanPlatform(ctx, name, sink)
		if err != nil {
			return nil, err
		}
		sink.Emit(cluster.Done, "plan", "", "%s", diff.Summary.String())
		return diff, nil
	})
}

func (s *Server) handlePlatformApply(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.startOp(w, name, "platform.apply", nil, func(ctx context.Context, sink cluster.Sink) (any, error) {
		return nil, s.manager.ApplyPlatform(ctx, name, sink)
	})
}

func (s *Server) handlePlatformApplyPlan(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	planID, _ := strconv.ParseInt(r.PathValue("planId"), 10, 64)
	planOp, err := s.store.GetOperation(r.Context(), planID)
	if err != nil {
		writeErr(w, err)
		return
	}
	if planOp.Cluster != name || planOp.Kind != "platform.plan" || planOp.Status != "done" || planOp.Artifact == nil {
		http.Error(w, "not a completed plan for this cluster", http.StatusBadRequest)
		return
	}
	var diff tofu.PlanDiff
	if err := json.Unmarshal(planOp.Artifact, &diff); err != nil {
		http.Error(w, fmt.Sprintf("plan #%d cannot be read; plan again", planID), http.StatusConflict)
		return
	}
	if latest := s.latestPlan(r.Context(), name); latest != planID {
		http.Error(w, fmt.Sprintf("plan #%d has been superseded by plan #%d; review the newer plan", planID, latest), http.StatusConflict)
		return
	}
	s.startOp(w, name, "platform.apply", map[string]any{"planId": planID}, func(ctx context.Context, sink cluster.Sink) (any, error) {
		return nil, s.manager.ApplyPlan(ctx, name, diff.Timestamp, sink)
	})
}

func (s *Server) latestPlan(ctx context.Context, name string) int64 {
	ops, err := s.store.ListOperations(ctx, 200)
	if err != nil {
		return 0
	}
	for _, op := range ops {
		if op.Cluster == name && op.Kind == "platform.plan" && op.Status == "done" {
			return op.ID
		}
	}
	return 0
}

func (s *Server) handleImageStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.manager.ImageStatus(r.Context(), r.PathValue("name"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleUpgradeTalos(w http.ResponseWriter, r *http.Request) {
	s.upgrade(w, r, "upgrade.talos", s.manager.UpgradeTalos)
}

func (s *Server) handleUpgradeKubernetes(w http.ResponseWriter, r *http.Request) {
	s.upgrade(w, r, "upgrade.kubernetes", s.manager.UpgradeKubernetes)
}

func (s *Server) upgrade(w http.ResponseWriter, r *http.Request, kind string, fn func(context.Context, string, string, cluster.Sink) error) {
	name := r.PathValue("name")
	var req struct {
		To string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.To == "" {
		http.Error(w, `body must be {"to": "<version>"}`, http.StatusBadRequest)
		return
	}
	s.startOp(w, name, kind, req, func(ctx context.Context, sink cluster.Sink) (any, error) {
		return nil, fn(ctx, name, req.To, sink)
	})
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req struct {
		Dir string `json:"dir"`
	}
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	if req.Dir == "" {
		req.Dir = s.manager.ClusterDir(name) + "/export"
	}
	if err := s.manager.Export(r.Context(), name, req.Dir); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"dir": req.Dir})
}

func (s *Server) handleNodeAdd(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var n config.Node
	if !decodeJSON(w, r, &n) {
		return
	}
	if n.MAC == "" {
		if row, err := s.store.GetNode(r.Context(), n.IP); err == nil {
			n.MAC = row.MAC
		}
	}
	id, err := s.startNodeAdd(name, n)
	accepted(w, id, err)
}

func (s *Server) startNodeAdd(name string, n config.Node) (int64, error) {
	return s.runOperation(name, "node.add", n, func(ctx context.Context, sink cluster.Sink) (any, error) {
		return nil, s.manager.AddNode(ctx, name, n, sink)
	})
}

func (s *Server) handleNodeRemove(w http.ResponseWriter, r *http.Request) {
	name, hostname := r.PathValue("name"), r.PathValue("hostname")
	force := r.URL.Query().Get("force") == "true"
	s.startOp(w, name, "node.remove", map[string]any{"hostname": hostname, "force": force}, func(ctx context.Context, sink cluster.Sink) (any, error) {
		return nil, s.manager.RemoveNode(ctx, name, hostname, cluster.RemoveOptions{Force: force}, sink)
	})
}

type nodeView struct {
	store.NodeRow
	Kind      store.Kind       `json:"kind"`
	Talos     bool             `json:"talos"`
	Inventory *talos.Inventory `json:"inventory,omitempty"`
}

func machineView(row store.NodeRow) nodeView {
	v := nodeView{NodeRow: row, Kind: row.Kind(), Talos: row.Talos()}
	if inv, ok := inventoryOf(&row); ok {
		v.Inventory = &inv
	}
	v.Hardware = nil
	if v.OOB != nil {
		c := *v.OOB
		if c.Password != "" {
			c.Password = store.Masked
		}
		v.OOB = &c
	}
	return v
}

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.ListNodes(r.Context(), r.URL.Query().Get("cluster"))
	if err != nil {
		writeErr(w, err)
		return
	}
	out := []nodeView{}
	for _, row := range rows {
		out = append(out, machineView(row))
	}
	writeJSON(w, http.StatusOK, out)
}

type discoverRequest struct {
	Targets []string `json:"targets"`
}

func (s *Server) handleDiscover(w http.ResponseWriter, r *http.Request) {
	var req discoverRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Targets) == 0 {
		http.Error(w, `body must be {"targets": ["cidr or ip", ...]}`, http.StatusBadRequest)
		return
	}
	id, err := s.startDiscover(req.Targets)
	accepted(w, id, err)
}

func (s *Server) startDiscover(targets []string) (int64, error) {
	addrs, err := talos.ExpandTargets(targets)
	if err != nil {
		return 0, err
	}
	return s.runOperation("", "discover", discoverRequest{Targets: targets}, func(ctx context.Context, sink cluster.Sink) (any, error) {
		sink.Plan(cluster.Steps("scan", fmt.Sprintf("Probe %d addresses on port 50000", len(addrs)), "record", "Record inventory", "amt", "Probe the rest for Intel AMT or a Redfish BMC")...)
		sink.Begin("scan")
		results := talos.Scan(ctx, addrs, 64, 2*time.Second)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		sink.End("scan")
		sink.Begin("record")
		found, err := cluster.RecordScan(ctx, s.store, results, func(res talos.ScanResult, vipOf string) {
			if vipOf != "" {
				sink.Emit(cluster.Info, "record", res.IP, "VIP of cluster %s, skipped", vipOf)
			} else {
				sink.Emit(cluster.Info, "record", res.IP, "%s", res.State)
			}
		})
		if err != nil {
			return nil, err
		}
		sink.Emit(cluster.Done, "record", "", "%d Talos nodes found", found)
		amtFound := s.discoverAMT(ctx, addrs, results, sink)
		return map[string]int{"found": found, "amt": amtFound}, nil
	})
}

func (s *Server) nodeClient(r *http.Request) (*talos.Client, error) {
	ip := r.PathValue("ip")
	row, err := s.store.GetNode(r.Context(), ip)
	if err != nil {
		if m, merr := s.store.GetMachine(r.Context(), ip); merr == nil {
			row, ip, err = m, m.IP, nil
		}
	}
	if err != nil {
		return nil, err
	}
	if row.IP == "" {
		return nil, &statusError{http.StatusConflict, "No address is known for this machine."}
	}
	if !row.Talos() {
		return nil, &statusError{http.StatusConflict, noTalosReason(row)}
	}
	if !talos.PortOpen(r.Context(), ip, 2*time.Second) {
		return nil, &statusError{http.StatusBadGateway, fmt.Sprintf("Talos API at %s:%s is not answering.", ip, talos.Port)}
	}
	if row.Cluster == "" {
		return talos.DialMaintenance(r.Context(), ip)
	}
	sec, err := s.store.GetClusterSecrets(r.Context(), row.Cluster)
	if err != nil {
		return nil, err
	}
	return talos.Dial(r.Context(), ip, sec.Talosconfig)
}

func (s *Server) handleNodeLogs(w http.ResponseWriter, r *http.Request) {
	tc, err := s.nodeClient(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer tc.Close()
	follow := r.URL.Query().Get("follow") == "true"
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	fl, _ := w.(http.Flusher)
	write := func(b []byte) {
		w.Write(b)
		if fl != nil {
			fl.Flush()
		}
	}
	ctx := tc.Context(r.Context())
	if svc := r.URL.Query().Get("service"); svc != "" {
		st, err := tc.Logs(ctx, "system", 0, svc, follow, 500)
		if err != nil {
			writeErr(w, err)
			return
		}
		for {
			m, err := st.Recv()
			if err != nil {
				return
			}
			write(m.Bytes)
		}
	}
	st, err := tc.Dmesg(ctx, follow, false)
	if err != nil {
		writeErr(w, err)
		return
	}
	for {
		m, err := st.Recv()
		if err != nil {
			return
		}
		write(m.Bytes)
	}
}

type serviceView struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	Healthy bool   `json:"healthy"`
	Unknown bool   `json:"unknown,omitempty"`
	Last    string `json:"last"`
}

func (s *Server) handleNodeServices(w http.ResponseWriter, r *http.Request) {
	tc, err := s.nodeClient(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer tc.Close()
	resp, err := tc.ServiceList(tc.Context(r.Context()))
	if err != nil {
		writeErr(w, err)
		return
	}
	out := []serviceView{}
	for _, m := range resp.Messages {
		for _, svc := range m.Services {
			v := serviceView{ID: svc.Id, State: svc.State}
			if svc.Health != nil {
				v.Healthy = svc.Health.Healthy
				v.Unknown = svc.Health.Unknown
				v.Last = svc.Health.LastMessage
			}
			if n := len(svc.Events.Events); n > 0 {
				v.Last = svc.Events.Events[n-1].Msg
			}
			out = append(out, v)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleNodeReboot(w http.ResponseWriter, r *http.Request) {
	tc, err := s.nodeClient(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer tc.Close()
	if err := tc.Reboot(tc.Context(r.Context())); err != nil {
		writeErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), "", "node.reboot", r.PathValue("ip"))
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleConfigValidate(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, err)
		return
	}
	c, err := config.Parse(body)
	if err != nil {
		writeErr(w, unprocessable(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"yaml": mustYAML(c), "cluster": c})
}

func (s *Server) handleConfigDraft(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string   `json:"name"`
		IPs  []string `json:"ips"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	c, err := s.draft(r, req.Name, req.IPs)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"yaml": mustYAML(c), "topology": config.Recommend(len(req.IPs))})
}

func (s *Server) draft(r *http.Request, name string, ips []string) (*config.Cluster, error) {
	if name == "" {
		name = "cluster"
	}
	topo := config.Recommend(len(ips))
	c := &config.Cluster{APIVersion: config.APIVersion, Kind: config.KindCluster, Metadata: config.Metadata{Name: name}}
	c.Spec.Platform = config.Platform{
		MetalLB: config.MetalLB{Enabled: true}, IngressNginx: config.Addon{Enabled: true},
		GVisor: config.Addon{Enabled: true}, MetricsServer: config.Addon{Enabled: true},
	}
	names := hostnamer{name: name}
	for i, ip := range ips {
		row, err := s.store.GetNode(r.Context(), ip)
		if err != nil {
			return nil, err
		}
		inv, _ := inventoryOf(row)
		n := config.Node{IP: ip, MAC: row.MAC, Arch: config.Arch(row.Arch), KVM: inv.KVM}
		names.assign(&n, i < topo.ControlPlanes)
		if cand := inv.InstallCandidates(); len(cand) > 0 {
			n.InstallDisk = config.InstallDisk{Path: cand[0].DevPath}
		} else {
			n.InstallDisk = config.InstallDisk{Path: "/dev/sda"}
		}
		c.Spec.Nodes = append(c.Spec.Nodes, n)
	}
	sched := topo.AllowScheduling
	c.Spec.ControlPlane.AllowScheduling = &sched
	if len(ips) > 0 {
		c.Spec.Platform.MetalLB.Range = config.DefaultMetalLBRange(ips[0])
	}
	return reparse(c)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type statusError struct {
	Status int
	Msg    string
}

func (e *statusError) Error() string { return e.Msg }

func noTalosReason(m *store.Machine) string {
	switch m.Kind() {
	case store.KindLabHost:
		if m.LabHost != nil && m.LabHost.Driver == labhost.DriverVFKit {
			return "This Mac runs the lab VMs; it has no Talos API."
		}
		return "This machine is a lab host running Debian; it has no Talos API."
	case store.KindConfigured:
		return "Runs Talos configured outside Kubit; no credentials to query it."
	case store.KindBooting:
		return "Waiting for Talos to come up."
	default:
		if m.IsLabVM() {
			return "The VM is off; start it from its lab host."
		}
		return "Not running Talos right now."
	}
}

func writeErr(w http.ResponseWriter, err error) {
	var se *statusError
	status := http.StatusInternalServerError
	msg := err.Error()
	switch {
	case errors.As(err, &se):
		status, msg = se.Status, se.Msg
	case errors.Is(err, store.ErrNotFound):
		status = http.StatusNotFound
	case grpcstatus.Code(err) != codes.Unknown && grpcstatus.Code(err) != codes.OK:
		status, msg = talos.HTTPStatus(err), talos.ShortGRPC(err).Error()
	case strings.Contains(msg, "already exists"), strings.Contains(msg, "must"), strings.Contains(msg, "required"):
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]string{"error": msg})
}

func spaHandler(root http.FileSystem) http.Handler {
	files := http.FileServer(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		if f, err := root.Open(r.URL.Path); err == nil && !strings.HasSuffix(r.URL.Path, "/") {
			f.Close()
			files.ServeHTTP(w, r)
			return
		}
		r.URL.Path = "/"
		files.ServeHTTP(w, r)
	})
}

func (s *Server) discoverAMT(ctx context.Context, addrs []netip.Addr, talosResults []talos.ScanResult, sink cluster.Sink) int {
	sink.Begin("amt")
	isTalos := map[string]bool{}
	for _, r := range talosResults {
		if r.Err == nil {
			isTalos[r.IP] = true
		}
	}
	var rest []netip.Addr
	for _, a := range addrs {
		if !isTalos[a.String()] {
			rest = append(rest, a)
		}
	}
	v, _ := s.store.GetSettings(ctx)
	found := 0
	for _, r := range oob.Scan(ctx, rest, v.AMT, v.BMC, 2*time.Second) {
		label := oob.Label(r.Type)
		if r.MAC == "" {
			switch {
			case r.Err != nil:
				sink.Emit(cluster.Warn, "amt", r.IP, "%s answers but the default credentials were refused: %v", label, r.Err)
			case r.Type == "redfish":
				sink.Emit(cluster.Info, "amt", r.IP, "Redfish BMC answers; set default BMC credentials under Kubit settings, or add it by address on the Inventory page")
			default:
				sink.Emit(cluster.Warn, "amt", r.IP, "answers on %s but its MAC is unknown (not on this segment?); add it via its address on the Inventory page", oob.AMTPort)
			}
			continue
		}
		row := store.NodeRow{IP: r.IP, MAC: r.MAC, Source: r.Type, State: "amt"}
		if r.Type == "redfish" {
			row.IP = ""
		}
		existing, err := s.store.GetMachine(ctx, r.MAC)
		known := err == nil && existing.State != "" && existing.State != "amt"
		if known {
			row.State, row.Source = existing.State, existing.Source
			if existing.IP != "" {
				row.IP = existing.IP
			}
		}
		if r.Info != nil {
			row.Serial, row.UUID = r.Info.Serial, r.Info.UUID
			if r.Info.Model != "" && (err != nil || len(existing.Hardware) <= 2) {
				row.Hardware = oobHardware(*r.Info)
			}
		}
		if err := s.store.UpsertNode(ctx, row); err != nil {
			continue
		}
		if r.Info != nil {
			c := v.AMT
			if r.Type == "redfish" {
				c = v.BMC
			}
			c.Type, c.Host = r.Type, r.IP
			_ = s.store.SetMachineOOB(ctx, r.MAC, &c)
			sink.Emit(cluster.Info, "amt", r.IP, "%s %s, %s, power %s", label, r.Info.Version, strings.TrimSpace(r.Info.Manufacturer+" "+r.Info.Model), r.Info.Power)
		} else if r.Err != nil {
			sink.Emit(cluster.Warn, "amt", r.IP, "%s answers but the default credentials were refused: %v", label, r.Err)
		} else {
			sink.Emit(cluster.Info, "amt", r.IP, "%s answers; set default credentials under Kubit settings to identify it", label)
		}
		found++
	}
	sink.End("amt")
	if found > 0 {
		sink.Emit(cluster.Done, "amt", "", "%d machine(s) reachable out of band", found)
	}
	return found
}
