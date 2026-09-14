package api

import (
	"encoding/json"
	"net/http"

	"github.com/mikael/kubit/internal/talos"
)

func (s *Server) nodeRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/nodes/{ip}/inventory", s.handleNodeInventory)
	r.HandleFunc("GET /api/v1/nodes/{ip}/kubernetes", s.handleNodeKubernetes)
	r.HandleFunc("POST /api/v1/clusters/{name}/nodes/{hostname}/cordon", s.nodeOp("node.cordon", s.manager.CordonNode))
	r.HandleFunc("POST /api/v1/clusters/{name}/nodes/{hostname}/uncordon", s.nodeOp("node.uncordon", s.manager.UncordonNode))
	r.HandleFunc("POST /api/v1/clusters/{name}/nodes/{hostname}/drain", s.nodeOp("node.drain", s.manager.DrainNode))
	r.HandleFunc("POST /api/v1/clusters/{name}/nodes/{hostname}/reboot", s.handleNodeRebootOp)
	r.HandleFunc("POST /api/v1/clusters/{name}/nodes/{hostname}/upgrade", s.handleNodeUpgrade)
}

// handleNodeInventory returns live hardware/OS facts; for cluster members it also
// carries the etcd member view on control planes.
func (s *Server) handleNodeInventory(w http.ResponseWriter, r *http.Request) {
	tc, err := s.nodeClient(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer tc.Close()
	inv, err := tc.Inspect(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	if row, err := s.store.GetNode(r.Context(), r.PathValue("ip")); err == nil && row.Role == "controlplane" {
		inv.Etcd, _ = tc.EtcdMemberInfo(r.Context())
	}
	writeJSON(w, http.StatusOK, inv)
}

func (s *Server) handleNodeKubernetes(w http.ResponseWriter, r *http.Request) {
	row, err := s.store.GetNode(r.Context(), r.PathValue("ip"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if row.Cluster == "" || row.Hostname == "" {
		http.Error(w, "node is not a cluster member", http.StatusNotFound)
		return
	}
	kc, err := s.manager.KubeClient(r.Context(), row.Cluster)
	if err != nil {
		writeErr(w, err)
		return
	}
	d, err := kc.NodeDetail(r.Context(), row.Hostname)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) nodeOp(kind string, fn func(ctx contextT, name, hostname string, sink clusterSink) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name, hostname := r.PathValue("name"), r.PathValue("hostname")
		id, err := s.runOperation(name, kind, map[string]string{"hostname": hostname}, func(ctx contextT, sink clusterSink) (any, error) {
			return nil, fn(ctx, name, hostname, sink)
		})
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"operationId": id})
	}
}

func (s *Server) handleNodeRebootOp(w http.ResponseWriter, r *http.Request) {
	name, hostname := r.PathValue("name"), r.PathValue("hostname")
	var req struct {
		Drain bool `json:"drain"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	id, err := s.runOperation(name, "node.reboot", map[string]any{"hostname": hostname, "drain": req.Drain}, func(ctx contextT, sink clusterSink) (any, error) {
		return nil, s.manager.RebootNode(ctx, name, hostname, req.Drain, sink)
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"operationId": id})
}

func (s *Server) handleNodeUpgrade(w http.ResponseWriter, r *http.Request) {
	name, hostname := r.PathValue("name"), r.PathValue("hostname")
	var req struct {
		To string `json:"to"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	id, err := s.runOperation(name, "node.upgrade", map[string]any{"hostname": hostname, "to": req.To}, func(ctx contextT, sink clusterSink) (any, error) {
		return nil, s.manager.UpgradeNode(ctx, name, hostname, req.To, sink)
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"operationId": id})
}

var _ = talos.Port
