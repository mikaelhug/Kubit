package tofu

import (
	"embed"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/mikael/kubit/internal/config"
)

//go:embed all:templates
var templates embed.FS

// Render writes the platform root into dir: the static .tf files plus
// terraform.tfvars.json derived from cluster.yaml. State files already in dir are kept.
func Render(dir string, c *config.Cluster, kubeconfigPath string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	entries, err := fs.ReadDir(templates, "templates/platform")
	if err != nil {
		return err
	}
	current := map[string]bool{}
	for _, e := range entries {
		current[e.Name()] = true
		b, err := templates.ReadFile("templates/platform/" + e.Name())
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), b, 0o600); err != nil {
			return err
		}
	}
	existing, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range existing {
		if filepath.Ext(e.Name()) == ".tf" && !current[e.Name()] {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
	}
	vars := Vars(c, kubeconfigPath)
	b, err := json.MarshalIndent(vars, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "terraform.tfvars.json"), append(b, '\n'), 0o600)
}

type addonVars struct {
	Enabled bool           `json:"enabled"`
	Values  map[string]any `json:"values"`
}

type longhornVars struct {
	Enabled  bool           `json:"enabled"`
	Values   map[string]any `json:"values"`
	Replicas int            `json:"replicas"`
}

type fluxVars struct {
	Enabled    bool                   `json:"enabled"`
	Values     map[string]any         `json:"values"`
	Repository *config.FluxRepository `json:"repository"`
}

type buildsVars struct {
	Enabled bool   `json:"enabled"`
	IP      string `json:"ip"`
}

type MetallbVars struct {
	Enabled bool           `json:"enabled"`
	Range   string         `json:"range"`
	Pool    string         `json:"pool"`
	Values  map[string]any `json:"values"`
}

func vals(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// metallbDefaults keep MetalLB to what Kubit configures: layer-2 announcements need
// the speaker only, not the FRR-K8s BGP daemonset the chart enables by default; the
// requests make its pods Burstable so a starved node evicts BestEffort work first.
var metallbDefaults = map[string]any{
	"frrk8s":     map[string]any{"enabled": false},
	"speaker":    map[string]any{"frr": map[string]any{"enabled": false}, "resources": requests("20m", "64Mi")},
	"controller": map[string]any{"resources": requests("20m", "64Mi")},
}

var ingressDefaults = map[string]any{
	"controller": map[string]any{"replicaCount": 1, "resources": requests("50m", "128Mi")},
}

var metricsDefaults = map[string]any{
	"resources": requests("20m", "48Mi"),
}

func requests(cpu, mem string) map[string]any {
	return map[string]any{"requests": map[string]any{"cpu": cpu, "memory": mem}}
}

// merged lays the user's values over Kubit's defaults, map by map, so a single
// overridden key keeps its siblings.
func merged(defaults, over map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range defaults {
		out[k] = v
	}
	for k, v := range over {
		if dm, ok := out[k].(map[string]any); ok {
			if om, ok := v.(map[string]any); ok {
				out[k] = merged(dm, om)
				continue
			}
		}
		out[k] = v
	}
	return out
}

// Vars maps cluster.yaml's platform section onto the module's input variables.
func Vars(c *config.Cluster, kubeconfigPath string) map[string]any {
	p := c.Spec.Platform
	return map[string]any{
		"kubeconfig":       kubeconfigPath,
		"metallb":          MetallbVars{Enabled: p.MetalLB.Enabled, Range: p.MetalLB.Range, Pool: c.MetalLBPool(), Values: merged(metallbDefaults, p.MetalLB.Values)},
		"ingress_nginx":    addonVars{p.IngressNginx.Enabled, merged(ingressDefaults, p.IngressNginx.Values)},
		"gvisor":           addonVars{p.GVisor.Enabled, vals(p.GVisor.Values)},
		"metrics_server":   addonVars{p.MetricsServer.Enabled, merged(metricsDefaults, p.MetricsServer.Values)},
		"cert_manager":     addonVars{p.CertManager.Enabled, vals(p.CertManager.Values)},
		"builds":           buildsVars{Enabled: p.Builds.Enabled, IP: c.RegistryIP()},
		"flux":             fluxVars{Enabled: p.Flux.Enabled, Values: vals(p.Flux.Values), Repository: p.Flux.Repository},
		"longhorn":         longhornVars{Enabled: p.Longhorn.Enabled, Values: vals(p.Longhorn.Values), Replicas: c.LonghornReplicas()},
		"oidc_admin_group": c.Spec.Auth.AdminGroupSubject(),
	}
}

const (
	SOPSNamespace = "flux-system"
	SOPSSecret    = "sops-age"
	SOPSSecretKey = "age.agekey"
)
