package api

import (
	"encoding/json"
	"github.com/mikael/kubit/internal/config"
	"net/http"
	"strings"
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
	Nameservers       []string `json:"nameservers"`
	NTP               []string `json:"ntp"`
	// Etcd snapshot schedule; empty interval keeps the stored value.
	EtcdSnapshotInterval string `json:"etcdSnapshotInterval"`
	EtcdSnapshotKeep     int    `json:"etcdSnapshotKeep"`
	MaintenanceWindow    string `json:"maintenanceWindow"`
	MaintenanceTimezone  string `json:"maintenanceTimezone"`
	// OIDC is the API server's SSO; a nil or empty issuer removes it.
	OIDC *config.ClusterOIDC `json:"oidc"`
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
	c.Spec.Network.Nameservers = f.Nameservers
	c.Spec.Network.NTP = f.NTP
	if f.EtcdSnapshotInterval != "" {
		c.Spec.Backup.Etcd.Interval = f.EtcdSnapshotInterval
	}
	if f.EtcdSnapshotKeep > 0 {
		c.Spec.Backup.Etcd.Keep = f.EtcdSnapshotKeep
	}
	c.Spec.Maintenance.Window = strings.TrimSpace(f.MaintenanceWindow)
	c.Spec.Maintenance.Timezone = strings.TrimSpace(f.MaintenanceTimezone)
	if f.OIDC != nil && strings.TrimSpace(f.OIDC.Issuer) != "" {
		c.Spec.Auth.OIDC = f.OIDC
	} else {
		c.Spec.Auth.OIDC = nil
	}
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
