package api

import (
	"fmt"
	"net/http"

	"github.com/mikael/kubit/internal/config"
)

func (s *Server) addonRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/clusters/{name}/addons", s.handleAddons)
}

type addonUpdate struct {
	Enabled    *bool                  `json:"enabled,omitempty"`
	Range      *string                `json:"range,omitempty"`
	ValuesYAML *string                `json:"valuesYaml,omitempty"`
	Repository *config.FluxRepository `json:"repository,omitempty"`
}

func (req addonUpdate) apply(p *config.Platform, key string, values map[string]any) error {
	set := func(a *config.Addon) {
		if req.Enabled != nil {
			a.Enabled = *req.Enabled
		}
		if req.ValuesYAML != nil {
			a.Values = values
		}
	}
	switch key {
	case "metallb":
		if req.Enabled != nil {
			p.MetalLB.Enabled = *req.Enabled
		}
		if req.Range != nil {
			p.MetalLB.Range = *req.Range
		}
		if req.ValuesYAML != nil {
			p.MetalLB.Values = values
		}
	case "traefik":
		set(&p.Traefik)
	case "gvisor":
		set(&p.GVisor)
	case "metricsServer":
		set(&p.MetricsServer)
	case "certManager":
		set(&p.CertManager)
	case "flux":
		if req.Enabled != nil {
			p.Flux.Enabled = *req.Enabled
		}
		if req.Repository != nil {
			req.Repository.Default()
			p.Flux.Repository = req.Repository
			if req.Repository.URL == "" {
				p.Flux.Repository = nil
			}
		}
		if req.ValuesYAML != nil {
			p.Flux.Values = values
		}
	case "longhorn":
		set(&p.Longhorn)
	case "builds":
		set(&p.Builds)
	default:
		return &statusError{Status: http.StatusNotFound, Msg: fmt.Sprintf("unknown add-on %q", key)}
	}
	return nil
}

func (s *Server) handleAddons(w http.ResponseWriter, r *http.Request) {
	list, err := s.manager.Addons(r.Context(), r.PathValue("name"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}
