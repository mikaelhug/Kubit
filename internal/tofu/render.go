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
	for _, e := range entries {
		b, err := templates.ReadFile("templates/platform/" + e.Name())
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), b, 0o600); err != nil {
			return err
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

type metallbVars struct {
	Enabled bool           `json:"enabled"`
	Range   string         `json:"range"`
	Values  map[string]any `json:"values"`
}

func vals(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// Vars maps cluster.yaml's platform section onto the module's input variables.
func Vars(c *config.Cluster, kubeconfigPath string) map[string]any {
	p := c.Spec.Platform
	return map[string]any{
		"kubeconfig":     kubeconfigPath,
		"metallb":        metallbVars{Enabled: p.MetalLB.Enabled, Range: p.MetalLB.Range, Values: vals(p.MetalLB.Values)},
		"ingress_nginx":  addonVars{p.IngressNginx.Enabled, vals(p.IngressNginx.Values)},
		"gvisor":         addonVars{p.GVisor.Enabled, vals(p.GVisor.Values)},
		"metrics_server": addonVars{p.MetricsServer.Enabled, vals(p.MetricsServer.Values)},
		"cert_manager":   addonVars{p.CertManager.Enabled, vals(p.CertManager.Values)},
		"argocd":         addonVars{p.ArgoCD.Enabled, vals(p.ArgoCD.Values)},
	}
}
