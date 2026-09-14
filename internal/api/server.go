// Package api is the HTTP face of Kubit: a JSON API for the SPA and scripts, an SSE
// stream of operation events, and the embedded web UI.
package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
	"github.com/mikael/kubit/internal/tofu"
	"github.com/mikael/kubit/internal/watch"
	"github.com/mikael/kubit/web"
)

type Server struct {
	mux     *http.ServeMux
	version string
	manager *cluster.Manager
	store   *store.Store
	hub     *hub
	locks   clusterLocks
	cancels sync.Map // operation id → context.CancelFunc
	watcher *watch.Watcher
	crypto  *store.Crypto
	// token, when set, is required as "Authorization: Bearer" on /api (non-loopback binds).
	token string
}

func New(version string, m *cluster.Manager, token string, crypto *store.Crypto) *Server {
	s := &Server{mux: http.NewServeMux(), version: version, manager: m, store: m.Store, hub: newHub(), token: token, crypto: crypto}
	if v, err := m.Store.GetSettings(contextBackground()); err == nil {
		m.Factory.BaseURL = v.FactoryURL
	}
	_ = s.store.MarkStaleOperations(contextBackground())
	r := s.mux
	r.HandleFunc("GET /api/v1/version", s.handleVersion)
	r.HandleFunc("GET /api/v1/events", s.handleEvents)
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
	r.HandleFunc("POST /api/v1/clusters/{name}/apply", s.handleClusterApply)
	r.HandleFunc("GET /api/v1/clusters/{name}/addons", s.handleAddons)
	r.HandleFunc("PUT /api/v1/clusters/{name}/addons/{addon}", s.handleAddonUpdate)
	r.HandleFunc("POST /api/v1/clusters/{name}/platform/plan", s.handlePlatformPlan)
	r.HandleFunc("POST /api/v1/clusters/{name}/platform/apply", s.handlePlatformApply)
	r.HandleFunc("POST /api/v1/clusters/{name}/platform/apply/{planId}", s.handlePlatformApplyPlan)
	r.HandleFunc("POST /api/v1/clusters/{name}/upgrade/talos", s.handleUpgradeTalos)
	r.HandleFunc("POST /api/v1/clusters/{name}/upgrade/kubernetes", s.handleUpgradeKubernetes)
	r.HandleFunc("POST /api/v1/clusters/{name}/export", s.handleExport)
	r.HandleFunc("POST /api/v1/clusters/{name}/nodes", s.handleNodeAdd)
	r.HandleFunc("DELETE /api/v1/clusters/{name}/nodes/{hostname}", s.handleNodeRemove)
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
	dist, _ := fs.Sub(web.Dist, "dist")
	r.Handle("/", spaHandler(http.FS(dist)))
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.token != "" && strings.HasPrefix(r.URL.Path, "/api/") {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got == "" {
			got = r.URL.Query().Get("token") // EventSource cannot set headers
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}
	s.mux.ServeHTTP(w, r)
}

// Loopback reports whether addr binds only to a loopback interface, in which case no
// token is required.
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
	writeJSON(w, http.StatusOK, map[string]string{"kubit": s.version})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	ch, cancel := s.hub.subscribe()
	defer cancel()
	fmt.Fprint(w, ": connected\n\n")
	fl.Flush()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		case m := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", marshal(m))
			fl.Flush()
		}
	}
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

// handleOperationRetry re-runs a finished operation with its stored request; only kinds
// whose request fully describes them can be retried.
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
		newID, err = s.runOperation(c.Metadata.Name, op.Kind, req, func(ctx contextT, sink clusterSink) (any, error) {
			if err := s.manager.Create(ctx, c, sink); err != nil {
				return nil, err
			}
			if req.SkipPlatform {
				return nil, nil
			}
			return nil, s.manager.ApplyPlatform(ctx, c.Metadata.Name, sink)
		})
	case "node.add":
		var n config.Node
		_ = json.Unmarshal(op.Request, &n)
		newID, err = s.runOperation(op.Cluster, op.Kind, n, func(ctx contextT, sink clusterSink) (any, error) {
			return nil, s.manager.AddNode(ctx, op.Cluster, n, sink)
		})
	case "platform.apply", "platform.plan":
		newID, err = s.runOperation(op.Cluster, op.Kind, nil, func(ctx contextT, sink clusterSink) (any, error) {
			if op.Kind == "platform.plan" {
				return s.manager.PlanPlatform(ctx, op.Cluster, sink)
			}
			return nil, s.manager.ApplyPlatform(ctx, op.Cluster, sink)
		})
	case "discover":
		var req struct {
			Targets []string `json:"targets"`
		}
		_ = json.Unmarshal(op.Request, &req)
		body, _ := json.Marshal(req)
		r2 := r.Clone(r.Context())
		r2.Body = io.NopCloser(strings.NewReader(string(body)))
		s.handleDiscover(w, r2)
		return
	default:
		http.Error(w, "this kind of operation cannot be retried; start it again from its page", http.StatusBadRequest)
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"operationId": newID})
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

// handleClusterYAMLSave stores an edited declaration without touching the cluster; the
// operator then applies node configs and plans the platform layer explicitly.
func (s *Server) handleClusterYAMLSave(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, err)
		return
	}
	updated, err := config.Parse(body)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	c, row, err := s.manager.LoadCluster(r.Context(), name)
	if err != nil {
		writeErr(w, err)
		return
	}
	if updated.Metadata.Name != name {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": fmt.Sprintf("declaration names cluster %q", updated.Metadata.Name)})
		return
	}
	updated.Spec.SchematicID = c.Spec.SchematicID
	if err := s.manager.SaveCluster(r.Context(), updated, row.State); err != nil {
		writeErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), name, "cluster.yaml.save", "")
	out, _ := updated.Marshal()
	writeJSON(w, http.StatusOK, map[string]string{"yaml": string(out)})
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

// createRequest carries cluster.yaml as text: the UI edits YAML, not a JSON mirror.
type createRequest struct {
	YAML         string `json:"yaml"`
	SkipPlatform bool   `json:"skipPlatform"`
}

func (s *Server) handleClusterCreate(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	c, err := config.Parse([]byte(req.YAML))
	if err != nil {
		writeErr(w, err)
		return
	}
	id, err := s.runOperation(c.Metadata.Name, "cluster.create", req, func(ctx contextT, sink clusterSink) (any, error) {
		if err := s.manager.Create(ctx, c, sink); err != nil {
			return nil, err
		}
		if req.SkipPlatform {
			return nil, nil
		}
		return nil, s.manager.ApplyPlatform(ctx, c.Metadata.Name, sink)
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"operationId": id, "cluster": c.Metadata.Name})
}

func (s *Server) handleClusterForget(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
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
	_ = json.NewDecoder(r.Body).Decode(&req)
	id, err := s.runOperation(name, "cluster.apply", req, func(ctx contextT, sink clusterSink) (any, error) {
		c, row, err := s.manager.LoadCluster(ctx, name)
		if err != nil {
			return nil, err
		}
		if req.YAML != "" {
			updated, err := config.Parse([]byte(req.YAML))
			if err != nil {
				return nil, err
			}
			if updated.Metadata.Name != name {
				return nil, fmt.Errorf("declaration names cluster %q", updated.Metadata.Name)
			}
			updated.Spec.SchematicID = c.Spec.SchematicID
			if err := s.manager.SaveCluster(ctx, updated, row.State); err != nil {
				return nil, err
			}
			c = updated
		}
		return nil, s.manager.ApplyConfigs(ctx, c, "", sink)
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"operationId": id})
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
	id, err := s.runOperation(name, "platform.plan", nil, func(ctx contextT, sink clusterSink) (any, error) {
		diff, err := s.manager.PlanPlatform(ctx, name, sink)
		if err != nil {
			return nil, err
		}
		sink(clusterEvent{Time: time.Now(), Kind: "log", Level: cluster.Done, Step: "plan", Message: diff.Summary.String()})
		return diff, nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"operationId": id})
}

func (s *Server) handlePlatformApply(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	id, err := s.runOperation(name, "platform.apply", nil, func(ctx contextT, sink clusterSink) (any, error) {
		return nil, s.manager.ApplyPlatform(ctx, name, sink)
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"operationId": id})
}

// handlePlatformApplyPlan applies the plan reviewed in operation {planId}.
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
	_ = json.Unmarshal(planOp.Artifact, &diff)
	if latest := s.latestPlan(r.Context(), name); latest != planID {
		http.Error(w, fmt.Sprintf("plan #%d has been superseded by plan #%d; review the newer plan", planID, latest), http.StatusConflict)
		return
	}
	id, err := s.runOperation(name, "platform.apply", map[string]any{"planId": planID}, func(ctx contextT, sink clusterSink) (any, error) {
		return nil, s.manager.ApplyPlan(ctx, name, diff.Timestamp, sink)
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"operationId": id})
}

// latestPlan returns the id of the most recent completed platform.plan for a cluster.
func (s *Server) latestPlan(ctx contextT, name string) int64 {
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

func (s *Server) handleUpgradeTalos(w http.ResponseWriter, r *http.Request) {
	s.upgrade(w, r, "upgrade.talos", s.manager.UpgradeTalos)
}

func (s *Server) handleUpgradeKubernetes(w http.ResponseWriter, r *http.Request) {
	s.upgrade(w, r, "upgrade.kubernetes", s.manager.UpgradeKubernetes)
}

func (s *Server) upgrade(w http.ResponseWriter, r *http.Request, kind string, fn func(contextT, string, string, clusterSink) error) {
	name := r.PathValue("name")
	var req struct {
		To string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.To == "" {
		http.Error(w, `body must be {"to": "<version>"}`, http.StatusBadRequest)
		return
	}
	id, err := s.runOperation(name, kind, req, func(ctx contextT, sink clusterSink) (any, error) {
		return nil, fn(ctx, name, req.To, sink)
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"operationId": id})
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req struct {
		Dir string `json:"dir"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
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
	if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
		writeErr(w, err)
		return
	}
	if n.MAC == "" {
		if row, err := s.store.GetNode(r.Context(), n.IP); err == nil {
			n.MAC = row.MAC
		}
	}
	id, err := s.runOperation(name, "node.add", n, func(ctx contextT, sink clusterSink) (any, error) {
		return nil, s.manager.AddNode(ctx, name, n, sink)
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"operationId": id})
}

func (s *Server) handleNodeRemove(w http.ResponseWriter, r *http.Request) {
	name, hostname := r.PathValue("name"), r.PathValue("hostname")
	force := r.URL.Query().Get("force") == "true"
	id, err := s.runOperation(name, "node.remove", map[string]any{"hostname": hostname, "force": force}, func(ctx contextT, sink clusterSink) (any, error) {
		return nil, s.manager.RemoveNode(ctx, name, hostname, cluster.RemoveOptions{Force: force}, sink)
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"operationId": id})
}

type nodeView struct {
	store.NodeRow
	Inventory *talos.Inventory `json:"inventory,omitempty"`
}

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.ListNodes(r.Context(), r.URL.Query().Get("cluster"))
	if err != nil {
		writeErr(w, err)
		return
	}
	out := []nodeView{}
	for _, row := range rows {
		v := nodeView{NodeRow: row}
		if len(row.Hardware) > 2 {
			var inv talos.Inventory
			if json.Unmarshal(row.Hardware, &inv) == nil {
				v.Inventory = &inv
			}
		}
		v.Hardware = nil
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDiscover(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Targets []string `json:"targets"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Targets) == 0 {
		http.Error(w, `body must be {"targets": ["cidr or ip", ...]}`, http.StatusBadRequest)
		return
	}
	addrs, err := talos.ExpandTargets(req.Targets)
	if err != nil {
		writeErr(w, err)
		return
	}
	id, err := s.runOperation("", "discover", req, func(ctx contextT, sink clusterSink) (any, error) {
		sink(clusterEvent{Time: time.Now(), Kind: "steps", Level: cluster.Info, Steps: cluster.Steps("scan", fmt.Sprintf("Probe %d addresses on port 50000", len(addrs)), "record", "Record inventory")})
		sink(clusterEvent{Time: time.Now(), Kind: "step", Step: "scan", Status: cluster.StepRunning})
		results := talos.Scan(ctx, addrs, 64, 2*time.Second)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		sink(clusterEvent{Time: time.Now(), Kind: "step", Step: "scan", Status: cluster.StepDone})
		sink(clusterEvent{Time: time.Now(), Kind: "step", Step: "record", Status: cluster.StepRunning})
		// A control-plane VIP answers on :50000 too, but it is an address, not a machine.
		vips := map[string]string{}
		if rows, err := s.store.ListClusters(ctx); err == nil {
			for _, row := range rows {
				if c, err := config.Parse(row.Spec); err == nil && c.Spec.ControlPlane.VIP != "" {
					vips[c.Spec.ControlPlane.VIP] = row.Name
				}
			}
		}
		found := 0
		for _, res := range results {
			if res.Err != nil {
				continue
			}
			if name, ok := vips[res.IP]; ok {
				sink(clusterEvent{Time: time.Now(), Kind: "log", Level: cluster.Info, Step: "record", Node: res.IP, Message: "VIP of cluster " + name + ", skipped"})
				continue
			}
			row := store.NodeRow{IP: res.IP, Source: "scan", State: string(res.State)}
			if inv := res.Inventory; inv != nil {
				row.MAC, row.Arch, row.TalosVersion = inv.PrimaryMAC(), inv.Arch, inv.TalosVersion
				row.UUID, row.Serial = inv.UUID, inv.Serial
				row.Hardware, _ = json.Marshal(inv)
			}
			if err := s.store.UpsertNode(ctx, row); err != nil {
				return nil, err
			}
			found++
			sink(clusterEvent{Time: time.Now(), Kind: "log", Level: cluster.Info, Step: "record", Node: res.IP, Message: string(res.State)})
		}
		sink(clusterEvent{Time: time.Now(), Kind: "log", Level: cluster.Done, Step: "record", Message: fmt.Sprintf("%d Talos nodes found", found)})
		return map[string]int{"found": found}, nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"operationId": id})
}

func (s *Server) nodeClient(r *http.Request) (*talos.Client, error) {
	ip := r.PathValue("ip")
	row, err := s.store.GetNode(r.Context(), ip)
	if err != nil {
		// Accept a MAC in place of the IP so machine pages can address by identity.
		if m, merr := s.store.GetMachine(r.Context(), ip); merr == nil && m.IP != "" {
			row, ip, err = m, m.IP, nil
		}
	}
	if err != nil {
		return nil, err
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

// handleNodeLogs streams dmesg (default) or a service log as text/plain; ?follow=true
// keeps the stream open.
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

// handleConfigValidate parses cluster.yaml text and returns the defaulted document.
func (s *Server) handleConfigValidate(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, err)
		return
	}
	c, err := config.Parse(body)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	out, _ := c.Marshal()
	writeJSON(w, http.StatusOK, map[string]any{"yaml": string(out), "cluster": c})
}

// handleConfigDraft builds a cluster.yaml from discovered nodes using the topology
// recommendation, for the create wizard to edit.
func (s *Server) handleConfigDraft(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string   `json:"name"`
		IPs  []string `json:"ips"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	c, err := s.draft(r, req.Name, req.IPs)
	if err != nil {
		writeErr(w, err)
		return
	}
	out, _ := c.Marshal()
	writeJSON(w, http.StatusOK, map[string]any{"yaml": string(out), "topology": config.Recommend(len(req.IPs))})
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
	cps, workers := 0, 0
	for i, ip := range ips {
		row, err := s.store.GetNode(r.Context(), ip)
		if err != nil {
			return nil, err
		}
		var inv talos.Inventory
		_ = json.Unmarshal(row.Hardware, &inv)
		n := config.Node{IP: ip, MAC: row.MAC, Arch: config.Arch(row.Arch), KVM: inv.KVM}
		if i < topo.ControlPlanes {
			cps++
			n.Role, n.Hostname = config.RoleControlPlane, fmt.Sprintf("%s-cp-%02d", name, cps)
		} else {
			workers++
			n.Role, n.Hostname = config.RoleWorker, fmt.Sprintf("%s-worker-%02d", name, workers)
		}
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
		// MetalLB range: the top of the first node's /24, a reasonable LAN default to edit.
		if ip := net.ParseIP(ips[0]).To4(); ip != nil {
			c.Spec.Platform.MetalLB.Range = fmt.Sprintf("%d.%d.%d.200-%d.%d.%d.220", ip[0], ip[1], ip[2], ip[0], ip[1], ip[2])
		}
	}
	b, err := c.Marshal()
	if err != nil {
		return nil, err
	}
	return config.Parse(b)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, store.ErrNotFound):
		status = http.StatusNotFound
	case strings.Contains(err.Error(), "already exists"), strings.Contains(err.Error(), "must"), strings.Contains(err.Error(), "required"):
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// spaHandler serves static assets and falls back to index.html for client-side routes.
func spaHandler(root http.FileSystem) http.Handler {
	files := http.FileServer(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f, err := root.Open(r.URL.Path); err == nil {
			f.Close()
			files.ServeHTTP(w, r)
			return
		}
		r.URL.Path = "/"
		files.ServeHTTP(w, r)
	})
}
