package api

import (
	"fmt"
	"net/http"

	"github.com/mikael/kubit/internal/config"
	"go.yaml.in/yaml/v4"
)

func (s *Server) addonRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/clusters/{name}/addons", s.handleAddons)
	r.HandleFunc("PUT /api/v1/clusters/{name}/addons/{addon}", s.handleAddonUpdate)
}

type addonUpdate struct {
	Enabled    *bool                  `json:"enabled,omitempty"`
	Range      *string                `json:"range,omitempty"`
	ValuesYAML *string                `json:"valuesYaml,omitempty"`
	Repository *config.FluxRepository `json:"repository,omitempty"`
}

func (s *Server) handleAddonUpdate(w http.ResponseWriter, r *http.Request) {
	name, key := r.PathValue("name"), r.PathValue("addon")
	var req addonUpdate
	if !decodeJSON(w, r, &req) {
		return
	}
	var values map[string]any
	if req.ValuesYAML != nil {
		if err := yaml.Unmarshal([]byte(*req.ValuesYAML), &values); err != nil {
			writeErr(w, &statusError{Status: http.StatusUnprocessableEntity, Msg: "values: " + err.Error()})
			return
		}
	}
	if _, ok := s.editCluster(w, r, "addon.update", key, func(c *config.Cluster) error {
		return req.apply(&c.Spec.Platform, key, values)
	}); !ok {
		return
	}
	list, err := s.manager.Addons(r.Context(), name)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
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
	case "ingressNginx":
		set(&p.IngressNginx)
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
