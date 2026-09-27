package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/store"
)

func (s *Server) certRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/clusters/{name}/certificates", s.handleCertificates)
	r.HandleFunc("POST /api/v1/clusters/{name}/certificates/rotate", s.handleCertRotate)
}

func (s *Server) handleCertificates(w http.ResponseWriter, r *http.Request) {
	certs, err := s.manager.Certificates(r.Context(), r.PathValue("name"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, certs)
}

func (s *Server) handleCertRotate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req struct {
		Which string `json:"which"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || (req.Which != "talosconfig" && req.Which != "kubeconfig") {
		writeErr(w, badRequest(`body must be {"which": "talosconfig" | "kubeconfig"}`))
		return
	}
	s.startOp(w, name, "cert.rotate", req, func(ctx context.Context, sink cluster.Sink) (any, error) {
		err := s.manager.RotateCredential(ctx, name, req.Which, sink)
		if err == nil {
			_ = s.store.ResolveEvents(ctx, name, req.Which, "cert.expiring")
		}
		return nil, err
	})
}

func (s *Server) checkCertificates(ctx context.Context, name string) {
	certs, err := s.manager.Certificates(ctx, name)
	if err != nil {
		return
	}
	for _, c := range certs {
		if c.Error != "" || c.DaysLeft > 30 {
			continue
		}
		if s.store.HasOpenEvent(ctx, name, c.Name, "cert.expiring") {
			continue
		}
		sev := "warn"
		if c.DaysLeft <= 7 {
			sev = "critical"
		}
		msg := fmt.Sprintf("%s expires in %d days (%s).", c.Name, c.DaysLeft, c.NotAfter.Format("2006-01-02"))
		if c.Rotatable {
			msg += " Rotate it under Settings → Credentials."
		} else {
			msg += " This CA cannot be rotated by Kubit; plan a cluster rebuild before it expires."
		}
		s.raiseEvent(ctx, store.EventRow{Cluster: name, Node: c.Name, Severity: sev, Kind: "cert.expiring", Message: msg})
	}
}
