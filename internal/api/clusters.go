package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/tofu"
)

func (s *Server) clusterRoutes() {
	r := s.mux
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
	r.HandleFunc("POST /api/v1/clusters/{name}/platform/plan", s.handlePlatformPlan)
	r.HandleFunc("POST /api/v1/clusters/{name}/platform/apply", s.handlePlatformApply)
	r.HandleFunc("POST /api/v1/clusters/{name}/platform/apply/{planId}", s.handlePlatformApplyPlan)
	r.HandleFunc("POST /api/v1/clusters/{name}/upgrade/talos", s.disruptive(s.handleUpgradeTalos))
	r.HandleFunc("GET /api/v1/clusters/{name}/image", s.handleImageStatus)
	r.HandleFunc("POST /api/v1/clusters/{name}/upgrade/kubernetes", s.disruptive(s.handleUpgradeKubernetes))
	r.HandleFunc("POST /api/v1/clusters/{name}/export", s.handleExport)
	r.HandleFunc("POST /api/v1/clusters/{name}/nodes", s.handleNodeAdd)
	r.HandleFunc("DELETE /api/v1/clusters/{name}/nodes/{hostname}", s.disruptive(s.handleNodeRemove))
	r.HandleFunc("POST /api/v1/config/validate", s.handleConfigValidate)
	r.HandleFunc("POST /api/v1/config/draft", s.handleConfigDraft)
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
	return s.store.LatestOperation(ctx, name, "platform.plan", "done")
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
