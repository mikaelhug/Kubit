package cluster

import (
	"context"
	"path/filepath"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/k8s"
	"github.com/mikael/kubit/internal/tofu"
)

// AddonStatus joins three sources for one add-on: the declaration (enabled, values),
// the tofu state (what Helm release is recorded), and Kubernetes readiness.
type AddonStatus struct {
	Key       string         `json:"key"`
	Enabled   bool           `json:"enabled"`
	Values    map[string]any `json:"values,omitempty"`
	Pinned    string         `json:"pinnedVersion,omitempty"`
	Release   *tofu.Release  `json:"release,omitempty"`
	Readiness *k8s.Readiness `json:"readiness,omitempty"`
	// State summarises: disabled | pending | deploying | ready | degraded | failed | orphaned
	State string `json:"state"`
}

// addonMeta ties a cluster.yaml key to the namespace its workloads live in and the
// chart pin from the templates.
var addonMeta = []struct{ key, namespace, pin string }{
	{"metallb", "metallb-system", "0.16.1"},
	{"ingressNginx", "ingress-nginx", "4.15.1"},
	{"gvisor", "", ""},
	{"metricsServer", "kube-system", "3.14.0"},
	{"certManager", "cert-manager", "v1.21.2"},
	{"argocd", "argocd", "10.9.0"},
}

var addonTofuName = map[string]string{"metallb": "metallb", "ingressNginx": "ingress-nginx", "gvisor": "gvisor", "metricsServer": "metrics-server", "certManager": "cert-manager", "argocd": "argocd"}

func addonSpec(p config.Platform, key string) (bool, map[string]any) {
	switch key {
	case "metallb":
		return p.MetalLB.Enabled, p.MetalLB.Values
	case "ingressNginx":
		return p.IngressNginx.Enabled, p.IngressNginx.Values
	case "gvisor":
		return p.GVisor.Enabled, p.GVisor.Values
	case "metricsServer":
		return p.MetricsServer.Enabled, p.MetricsServer.Values
	case "certManager":
		return p.CertManager.Enabled, p.CertManager.Values
	case "argocd":
		return p.ArgoCD.Enabled, p.ArgoCD.Values
	}
	return false, nil
}

// Addons returns the status of every add-on for a cluster.
func (m *Manager) Addons(ctx context.Context, name string) ([]AddonStatus, error) {
	c, _, err := m.LoadCluster(ctx, name)
	if err != nil {
		return nil, err
	}
	var releases []tofu.Release
	dir := filepath.Join(m.ClusterDir(name), "infra", "platform")
	if bin, err := tofu.Binary(ctx, filepath.Join(m.Home, "bin")); err == nil {
		releases, _ = (&tofu.Runner{Bin: bin, Dir: dir}).Releases(ctx)
	}
	kc, kerr := m.KubeClient(ctx, name)
	out := make([]AddonStatus, 0, len(addonMeta))
	for _, meta := range addonMeta {
		enabled, values := addonSpec(c.Spec.Platform, meta.key)
		st := AddonStatus{Key: meta.key, Enabled: enabled, Values: values, Pinned: meta.pin}
		for i := range releases {
			if releases[i].Addon == addonTofuName[meta.key] {
				st.Release = &releases[i]
			}
		}
		if meta.namespace != "" && kerr == nil && (st.Release != nil || enabled) {
			if r, err := kc.NamespaceReadiness(ctx, meta.namespace); err == nil {
				st.Readiness = r
			}
		}
		st.State = addonState(st, meta.namespace == "")
		out = append(out, st)
	}
	return out, nil
}

func addonState(st AddonStatus, manifestOnly bool) string {
	switch {
	case !st.Enabled && st.Release == nil:
		return "disabled"
	case !st.Enabled && st.Release != nil:
		return "orphaned" // declared off, still installed: plan will remove it
	case manifestOnly:
		return "ready"
	case st.Release == nil:
		return "pending"
	case st.Release.Status != "deployed":
		return "failed"
	case st.Readiness == nil:
		return "deploying"
	case st.Readiness.Total > 0 && st.Readiness.Ready == st.Readiness.Total:
		return "ready"
	case st.Readiness.Ready > 0:
		return "degraded"
	default:
		return "deploying"
	}
}
