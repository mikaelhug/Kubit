package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/mikael/kubit/internal/netx"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
	"github.com/siderolabs/talos/pkg/machinery/api/common"
)

func (s *Server) nodeTalosRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/nodes", s.handleNodes)
	r.HandleFunc("GET /api/v1/nodes/{ip}/logs", s.handleNodeLogs)
	r.HandleFunc("GET /api/v1/nodes/{ip}/services", s.handleNodeServices)
}

type nodeView struct {
	store.Machine
	Kind      store.Kind       `json:"kind"`
	Talos     bool             `json:"talos"`
	Inventory *talos.Inventory `json:"inventory,omitempty"`
	Declared  *declaredNode    `json:"declared,omitempty"`
}

type declaredNode struct {
	Cluster  string `json:"cluster"`
	Hostname string `json:"hostname"`
}

func (s *Server) declaredNodes() map[string]declaredNode {
	out := map[string]declaredNode{}
	for _, r := range s.servedRepos() {
		d, err := s.manager.Desired(r.Cluster)
		if r.Cluster == "" || err != nil {
			continue
		}
		for _, n := range d.Cluster.Spec.Nodes {
			if n.MAC != "" {
				out[netx.MACKey(n.MAC)] = declaredNode{Cluster: r.Cluster, Hostname: n.Hostname}
			}
		}
	}
	return out
}

func (s *Server) machineView(row store.Machine, declared map[string]declaredNode) nodeView {
	v := nodeView{Machine: row, Kind: row.Kind(), Talos: row.Talos()}
	if d, ok := declared[row.MAC]; ok && row.Cluster == "" {
		v.Declared = &d
	}
	if inv, ok := inventoryOf(&row); ok {
		v.Inventory = &inv
	}
	v.Hardware = nil
	return v
}

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	out := []nodeView{}
	declared := s.declaredNodes()
	for _, row := range s.store.ListNodes(r.URL.Query().Get("cluster")) {
		out = append(out, s.machineView(row, declared))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) nodeClient(r *http.Request) (*talos.Client, *store.Machine, error) {
	ip := r.PathValue("ip")
	row, err := s.store.GetNode(ip)
	if err != nil {
		return nil, nil, err
	}
	if row.IP == "" {
		return nil, nil, conflict("No address is known for this machine.")
	}
	if !row.Talos() {
		return nil, nil, conflict(noTalosReason(row))
	}
	if !talos.PortOpen(r.Context(), ip, 2*time.Second) {
		return nil, nil, &statusError{Status: http.StatusBadGateway, Msg: fmt.Sprintf("Talos API at %s:%s is not answering.", ip, talos.Port)}
	}
	if row.Cluster == "" {
		tc, err := talos.DialMaintenance(r.Context(), ip)
		return tc, row, err
	}
	sec, err := s.store.GetClusterSecrets(row.Cluster)
	if err != nil {
		return nil, nil, err
	}
	tc, err := talos.Dial(r.Context(), ip, sec.Talosconfig)
	return tc, row, err
}

func (s *Server) handleNodeLogs(w http.ResponseWriter, r *http.Request) {
	tc, _, err := s.nodeClient(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer tc.Close()
	follow := r.URL.Query().Get("follow") == "true"
	var st interface{ Recv() (*common.Data, error) }
	if svc := r.URL.Query().Get("service"); svc != "" {
		st, err = tc.ServiceLog(r.Context(), svc, follow, 500)
	} else {
		st, err = tc.KernelLog(r.Context(), follow)
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	write := textStream(w)
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
	tc, _, err := s.nodeClient(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer tc.Close()
	services, err := tc.Services(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	out := []serviceView{}
	for _, svc := range services {
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
	writeJSON(w, http.StatusOK, out)
}

func noTalosReason(m *store.Machine) string {
	if m.Kind() == store.KindConfigured {
		return "Configured outside Kubit; no credentials."
	}
	return "Not answering on the Talos API."
}

func inventoryOf(m *store.Machine) (talos.Inventory, bool) {
	var inv talos.Inventory
	if len(m.Hardware) <= 2 || json.Unmarshal(m.Hardware, &inv) != nil {
		return inv, false
	}
	return inv, true
}
