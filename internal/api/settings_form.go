package api

import (
	"encoding/json"
	"net/http"
)

// clusterForm is the structured, non-node part of cluster.yaml the Settings form edits.
type clusterForm struct {
	TalosVersion      string   `json:"talosVersion"`
	KubernetesVersion string   `json:"kubernetesVersion"`
	Endpoint          string   `json:"endpoint"`
	VIP               string   `json:"vip"`
	AllowScheduling   *bool    `json:"allowScheduling"`
	PodCIDR           string   `json:"podCIDR"`
	ServiceCIDR       string   `json:"serviceCIDR"`
	Extensions        []string `json:"extensions"`
}

// handleClusterForm validates and saves the structured fields; versions are only
// recorded here — use the upgrade actions to move running nodes.
func (s *Server) handleClusterForm(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var f clusterForm
	if err := json.NewDecoder(r.Body).Decode(&f); err != nil {
		writeErr(w, err)
		return
	}
	c, row, err := s.manager.LoadCluster(r.Context(), name)
	if err != nil {
		writeErr(w, err)
		return
	}
	c.Spec.TalosVersion = f.TalosVersion
	c.Spec.KubernetesVersion = f.KubernetesVersion
	c.Spec.ControlPlane.Endpoint = f.Endpoint
	c.Spec.ControlPlane.VIP = f.VIP
	c.Spec.ControlPlane.AllowScheduling = f.AllowScheduling
	c.Spec.Network.PodCIDR = f.PodCIDR
	c.Spec.Network.ServiceCIDR = f.ServiceCIDR
	c.Spec.Extensions = f.Extensions
	if err := c.Validate(); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	if err := s.manager.SaveCluster(r.Context(), c, row.State); err != nil {
		writeErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), name, "cluster.form.save", "")
	out, _ := c.Marshal()
	writeJSON(w, http.StatusOK, map[string]string{"yaml": string(out)})
}
