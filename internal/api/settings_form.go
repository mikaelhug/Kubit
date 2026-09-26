package api

import (
	"net/http"
	"strings"

	"github.com/mikael/kubit/internal/config"
)

type clusterForm struct {
	TalosVersion         string              `json:"talosVersion"`
	KubernetesVersion    string              `json:"kubernetesVersion"`
	Endpoint             string              `json:"endpoint"`
	VIP                  string              `json:"vip"`
	AllowScheduling      *bool               `json:"allowScheduling"`
	PodCIDR              string              `json:"podCIDR"`
	ServiceCIDR          string              `json:"serviceCIDR"`
	Extensions           []string            `json:"extensions"`
	Nameservers          []string            `json:"nameservers"`
	NTP                  []string            `json:"ntp"`
	EtcdSnapshotInterval string              `json:"etcdSnapshotInterval"`
	EtcdSnapshotKeep     int                 `json:"etcdSnapshotKeep"`
	MaintenanceWindow    string              `json:"maintenanceWindow"`
	MaintenanceTimezone  string              `json:"maintenanceTimezone"`
	OIDC                 *config.ClusterOIDC `json:"oidc"`
}

func (s *Server) handleClusterForm(w http.ResponseWriter, r *http.Request) {
	var f clusterForm
	if !decodeJSON(w, r, &f) {
		return
	}
	c, ok := s.editCluster(w, r, "cluster.form.save", "", func(c *config.Cluster) error {
		f.apply(c)
		return nil
	})
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"yaml": mustYAML(c)})
}

func (f clusterForm) apply(c *config.Cluster) {
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
}
