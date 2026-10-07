package api

import (
	"net/http"
	"strings"

	"github.com/mikaelhug/kubit/internal/store"
)

func (s *Server) nodeRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/nodes/{ip}/inventory", s.handleNodeInventory)
	r.HandleFunc("GET /api/v1/nodes/{ip}/kubernetes", s.handleNodeKubernetes)
}

func (s *Server) handleNodeInventory(w http.ResponseWriter, r *http.Request) {
	tc, row, err := s.nodeClient(r)
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
	if !strings.HasPrefix(row.MAC, "ip:") {
		s.manager.RecordInventory(row.MAC, row.IP, inv)
	}
	if row.Role == "controlplane" {
		inv.Etcd, _ = tc.EtcdMemberInfo(r.Context())
	}
	writeJSON(w, http.StatusOK, inv)
}

func (s *Server) handleNodeKubernetes(w http.ResponseWriter, r *http.Request) {
	row, err := s.store.GetNode(r.PathValue("ip"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if row.Kind() != store.KindMember || row.Hostname == "" {
		writeErr(w, &statusError{Status: http.StatusNotFound, Msg: "node is not a cluster member"})
		return
	}
	kc, err := s.manager.KubeClient(r.Context(), row.Cluster)
	if err != nil {
		writeErr(w, err)
		return
	}
	d, err := kc.NodeDetail(r.Context(), row.Hostname)
	reply(w, d, err)
}
