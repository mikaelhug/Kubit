package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
)

func (s *Server) nodeTalosRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/nodes", s.handleNodes)
	r.HandleFunc("GET /api/v1/nodes/{ip}/logs", s.handleNodeLogs)
	r.HandleFunc("GET /api/v1/nodes/{ip}/services", s.handleNodeServices)
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
		return nil, conflict("No address is known for this machine.")
	}
	if !row.Talos() {
		return nil, conflict(noTalosReason(row))
	}
	if !talos.PortOpen(r.Context(), ip, 2*time.Second) {
		return nil, &statusError{Status: http.StatusBadGateway, Msg: fmt.Sprintf("Talos API at %s:%s is not answering.", ip, talos.Port)}
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
	ctx := r.Context()
	if svc := r.URL.Query().Get("service"); svc != "" {
		st, err := tc.ServiceLog(ctx, svc, follow, 500)
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
	st, err := tc.KernelLog(ctx, follow)
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
	switch m.Kind() {
	case store.KindConfigured:
		return "Configured outside Kubit; no credentials."
	case store.KindBooting:
		return "Waiting for Talos."
	default:
		return "Not running Talos."
	}
}
