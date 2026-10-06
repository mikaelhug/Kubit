package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/mikael/kubit/internal/watch"
)

func (s *Server) certRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/clusters/{name}/certificates", s.handleCertificates)
}

func (s *Server) handleCertificates(w http.ResponseWriter, r *http.Request) {
	certs, err := s.manager.Certificates(r.Context(), r.PathValue("name"))
	reply(w, certs, err)
}

const certCheckEvery = time.Hour

func (s *Server) checkCertificates(ctx context.Context, name string) {
	a := s.alerts()
	certs, err := s.manager.Certificates(ctx, name)
	if err != nil {
		return
	}
	s.certsMu.Lock()
	last, seen := s.certsSeen[name]
	due := !seen || time.Since(last) >= certCheckEvery
	if due {
		s.certsSeen[name] = time.Now()
	}
	s.certsMu.Unlock()
	if !due {
		return
	}
	notify := seen
	for _, c := range certs {
		if c.Error != "" {
			continue
		}
		if c.DaysLeft > 30 {
			a.Resolve(name, c.Name, "cert.expiring", watch.Event{Cluster: name, Node: c.Name, Severity: "info", Kind: "cert.renewed", Message: c.Name + " renewed"})
			continue
		}
		sev := "warn"
		if c.DaysLeft <= 7 {
			sev = "critical"
		}
		msg := fmt.Sprintf("%s expires in %d days (%s).", c.Name, c.DaysLeft, c.NotAfter.Format("2006-01-02"))
		if !c.Rotatable {
			msg += " Kubit cannot rotate this CA; rebuild the cluster before it expires."
		}
		a.Raise(watch.Event{Cluster: name, Node: c.Name, Severity: sev, Kind: "cert.expiring", Message: msg, Notify: notify})
	}
}
