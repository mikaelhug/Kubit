package cluster

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/k8s"
	"github.com/mikael/kubit/internal/tofu"
)

type AddonStatus struct {
	Key       string         `json:"key"`
	Enabled   bool           `json:"enabled"`
	Values    map[string]any `json:"values,omitempty"`
	Pinned    string         `json:"pinnedVersion,omitempty"`
	Address   string         `json:"address,omitempty"`
	Release   *tofu.Release  `json:"release,omitempty"`
	Readiness *k8s.Readiness `json:"readiness,omitempty"`
	State     string         `json:"state"`
}

var addonMeta = []struct{ key, tofu, namespace string }{
	{"metallb", "metallb", "metallb-system"},
	{"traefik", "traefik", "traefik"},
	{"gvisor", "gvisor", ""},
	{"metricsServer", "metrics-server", "kube-system"},
	{"certManager", "cert-manager", "cert-manager"},
	{"flux", "flux", "flux-system"},
	{"longhorn", "longhorn", "longhorn-system"},
	{"builds", "builds", "kubit-builds"},
}

func addonOf(ns string) (string, bool) {
	for _, a := range addonMeta {
		if a.namespace != "" && a.namespace == ns {
			return a.key, true
		}
	}
	return "", false
}

func PlatformNamespace(ns string) (string, bool) {
	switch ns {
	case "kube-system", "kube-public", "kube-node-lease":
		return "kubernetes", true
	case "ingress-nginx":
		return "traefik", true
	}
	return addonOf(ns)
}

func AddonNamespace(ns string) bool {
	_, ok := addonOf(ns)
	return ok
}

func addonSpec(p config.Platform, key string) (bool, map[string]any) {
	switch key {
	case "metallb":
		return p.MetalLB.Enabled, p.MetalLB.Values
	case "traefik":
		return p.Traefik.Enabled, p.Traefik.Values
	case "gvisor":
		return p.GVisor.Enabled, p.GVisor.Values
	case "metricsServer":
		return p.MetricsServer.Enabled, p.MetricsServer.Values
	case "certManager":
		return p.CertManager.Enabled, p.CertManager.Values
	case "flux":
		return p.Flux.Enabled, p.Flux.Values
	case "longhorn":
		return p.Longhorn.Enabled, p.Longhorn.Values
	case "builds":
		return p.Builds.Enabled, p.Builds.Values
	}
	return false, nil
}

func (m *Manager) Addons(ctx context.Context, name string) ([]AddonStatus, error) {
	c, _, err := m.LoadCluster(ctx, name)
	if err != nil {
		return nil, err
	}
	var releases []tofu.Release
	if bin, err := m.tofuBin(ctx); err == nil {
		releases, err = (&tofu.Runner{Bin: bin, Dir: m.platformDir(name)}).Releases(ctx)
		if m.addonErrorChanged(name, err) {
			log.Printf("add-ons %s: %v", name, err)
		}
	}
	kc, kerr := m.KubeClient(ctx, name)
	out := make([]AddonStatus, len(addonMeta))
	var wg sync.WaitGroup
	for i, meta := range addonMeta {
		enabled, values := addonSpec(c.Spec.Platform, meta.key)
		st := &out[i]
		pin := tofu.ChartVersions[meta.tofu]
		*st = AddonStatus{Key: meta.key, Enabled: enabled, Values: values, Pinned: pin}
		if meta.key == "builds" && c.RegistryIP() != "" {
			st.Address = net.JoinHostPort(c.RegistryIP(), fmt.Sprint(config.RegistryPort))
		}
		for i := range releases {
			if releases[i].Addon == meta.tofu {
				st.Release = &releases[i]
			}
		}
		chartless := pin == "" && meta.namespace != ""
		if meta.namespace == "" || kerr != nil || (st.Release == nil && !enabled && !chartless) {
			st.State = addonState(*st, meta.namespace == "", chartless)
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r, err := kc.NamespaceReadiness(ctx, meta.namespace); err == nil {
				st.Readiness = r
			}
			st.State = addonState(*st, false, chartless)
		}()
	}
	wg.Wait()
	return out, nil
}

func (m *Manager) addonErrorChanged(name string, err error) bool {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	m.addonErrMu.Lock()
	defer m.addonErrMu.Unlock()
	if m.addonErr[name] == msg {
		return false
	}
	if msg == "" {
		delete(m.addonErr, name)
		return false
	}
	if m.addonErr == nil {
		m.addonErr = map[string]string{}
	}
	m.addonErr[name] = msg
	return true
}

func addonState(st AddonStatus, manifestOnly, chartless bool) string {
	switch {
	case !st.Enabled && chartless && st.Readiness != nil && st.Readiness.Total > 0:
		return "orphaned"
	case !st.Enabled && st.Release == nil:
		return "disabled"
	case !st.Enabled && st.Release != nil:
		return "orphaned"
	case manifestOnly:
		return "ready"
	case chartless && (st.Readiness == nil || st.Readiness.Total == 0):
		return "pending"
	case !chartless && st.Release == nil:
		return "pending"
	case !chartless && st.Release.Status != "deployed":
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
