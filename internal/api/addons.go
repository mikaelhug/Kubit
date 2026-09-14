package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/mikael/kubit/internal/config"
	"go.yaml.in/yaml/v4"
)

type addonUpdate struct {
	Enabled    *bool   `json:"enabled,omitempty"`
	Range      *string `json:"range,omitempty"`
	ValuesYAML *string `json:"valuesYaml,omitempty"`
}

// handleAddonUpdate edits one add-on in cluster.yaml (enabled, MetalLB range, Helm
// values as YAML) and saves; nothing is applied until the operator plans.
func (s *Server) handleAddonUpdate(w http.ResponseWriter, r *http.Request) {
	name, key := r.PathValue("name"), r.PathValue("addon")
	var req addonUpdate
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	c, row, err := s.manager.LoadCluster(r.Context(), name)
	if err != nil {
		writeErr(w, err)
		return
	}
	var values map[string]any
	if req.ValuesYAML != nil {
		if err := yaml.Unmarshal([]byte(*req.ValuesYAML), &values); err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "values: " + err.Error()})
			return
		}
	}
	p := &c.Spec.Platform
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
	case "argocd":
		set(&p.ArgoCD)
	default:
		http.Error(w, fmt.Sprintf("unknown add-on %q", key), http.StatusNotFound)
		return
	}
	if err := c.Validate(); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	if err := s.manager.SaveCluster(r.Context(), c, row.State); err != nil {
		writeErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), name, "addon.update", key)
	list, err := s.manager.Addons(r.Context(), name)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}
