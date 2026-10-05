package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/mikael/kubit/internal/store"
)

func (s *Server) certRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/clusters/{name}/certificates", s.handleCertificates)
}

func (s *Server) handleCertificates(w http.ResponseWriter, r *http.Request) {
	certs, err := s.manager.Certificates(r.Context(), r.PathValue("name"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, certs)
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
