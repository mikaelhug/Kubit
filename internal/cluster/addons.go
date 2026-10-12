package cluster

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"

	"github.com/mikaelhug/kubit/internal/config"
	"github.com/mikaelhug/kubit/internal/k8s"
	"github.com/mikaelhug/kubit/internal/tofu"
)

type AddonStatus struct {
	Key       string           `json:"key"`
	Enabled   bool             `json:"enabled"`
	Installed bool             `json:"installed"`
	Pinned    string           `json:"pinnedVersion,omitempty"`
	Address   string           `json:"address,omitempty"`
	Release   *k8s.HelmRelease `json:"release,omitempty"`
	Readiness *k8s.Readiness   `json:"readiness,omitempty"`
	State     string           `json:"state"`
}

var addonMeta = []struct{ key, tofu, namespace, release string }{
	{"metallb", "metallb", "metallb-system", "metallb"},
	{"traefik", "traefik", "traefik", "traefik"},
	{"gvisor", "gvisor", "", ""},
	{"metricsServer", "metrics-server", "kube-system", "metrics-server"},
	{"certManager", "cert-manager", "cert-manager", "cert-manager"},
	{"flux", "flux", "flux-system", "flux"},
	{"longhorn", "longhorn", "longhorn-system", "longhorn"},
	{"builds", "builds", "kubit-builds", ""},
	{"backup", "talos-backup", "", ""},
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
	case config.BackupNamespace:
		return "backup", true
	}
	return addonOf(ns)
}

func AddonNamespace(ns string) bool {
	_, ok := addonOf(ns)
	return ok
}

func addonEnabled(c *config.Cluster, key string) bool {
	p := c.Spec.Platform
	switch key {
	case "backup":
		return c.Spec.Backup.Enabled()
	case "metallb":
		return p.MetalLB.Enabled
	case "traefik":
		return p.Traefik.Enabled
	case "gvisor":
		return p.GVisor.Enabled
	case "metricsServer":
		return p.MetricsServer.Enabled
	case "certManager":
		return p.CertManager.Enabled
	case "flux":
		return p.Flux.Enabled
	case "longhorn":
		return p.Longhorn.Enabled
	case "builds":
		return p.Builds.Enabled
	}
	return false
}

func (m *Manager) Addons(ctx context.Context, name string) ([]AddonStatus, error) {
	c, _, err := m.LoadCluster(name)
	if err != nil {
		return nil, err
	}
	kc, kerr := m.KubeClient(ctx, name)
	var (
		relMu   sync.Mutex
		relErrs []error
	)
	out := make([]AddonStatus, len(addonMeta))
	var wg sync.WaitGroup
	for i, meta := range addonMeta {
		enabled := addonEnabled(c, meta.key)
		st := &out[i]
		pin := tofu.ChartVersions[meta.tofu]
		*st = AddonStatus{Key: meta.key, Enabled: enabled, Pinned: pin}
		if meta.key == "builds" && c.RegistryIP() != "" {
			st.Address = net.JoinHostPort(c.RegistryIP(), fmt.Sprint(config.RegistryPort))
		}
		chartless := pin == "" && meta.namespace != ""
		if meta.namespace == "" && kerr == nil {
			st.Installed = kc.ManifestAddonInstalled(ctx, meta.key)
		}
		if meta.namespace == "" || kerr != nil {
			st.State = addonState(*st, meta.namespace == "", chartless)
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if meta.release != "" {
				rel, err := kc.HelmRelease(ctx, meta.namespace, meta.release)
				if err != nil {
					relMu.Lock()
					relErrs = append(relErrs, err)
					relMu.Unlock()
				}
				st.Release = rel
			}
			if st.Release == nil && !enabled && !chartless {
				st.State = addonState(*st, false, chartless)
				return
			}
			if r, err := kc.NamespaceReadiness(ctx, meta.namespace); err == nil {
				st.Readiness = r
			}
			st.Installed = st.Release != nil || chartless && st.Readiness != nil && st.Readiness.Total > 0
			st.State = addonState(*st, false, chartless)
		}()
	}
	wg.Wait()
	if err := errors.Join(relErrs...); m.addonErrorChanged(name, err) {
		log.Printf("add-ons %s: %v", name, err)
	}
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
	case manifestOnly && !st.Enabled && st.Installed:
		return "orphaned"
	case manifestOnly && !st.Enabled:
		return "disabled"
	case manifestOnly && st.Installed:
		return "ready"
	case manifestOnly:
		return "pending"
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
