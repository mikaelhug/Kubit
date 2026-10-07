package tofu

import (
	"embed"
	"encoding/json"
	"io/fs"
	"maps"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/mikaelhug/kubit/internal/config"
)

//go:embed all:templates
var templates embed.FS

const templateRoot = "templates/platform"

func Render(dir string, c *config.Cluster, kubeconfigPath, ingressIP string) error {
	err := fs.WalkDir(templates, templateRoot, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dst := filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(p, templateRoot)))
		if e.IsDir() {
			return os.MkdirAll(dst, 0o700)
		}
		b, err := templates.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o600)
	})
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(Vars(c, kubeconfigPath, ingressIP), "", "  ")
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

type backupVars struct {
	Enabled       bool     `json:"enabled"`
	Schedule      string   `json:"schedule,omitempty"`
	Bucket        string   `json:"bucket,omitempty"`
	Region        string   `json:"region,omitempty"`
	Endpoint      string   `json:"endpoint,omitempty"`
	Prefix        string   `json:"prefix,omitempty"`
	PathStyle     bool     `json:"path_style"`
	Compression   bool     `json:"compression"`
	AgeRecipients []string `json:"age_recipients"`
	Cluster       string   `json:"cluster,omitempty"`
}

func backupVarsOf(c *config.Cluster) backupVars {
	b := c.Spec.Backup
	if !b.Enabled() {
		return backupVars{AgeRecipients: []string{}}
	}
	prefix := b.S3.Prefix
	if prefix == "" {
		prefix = c.Metadata.Name
	}
	return backupVars{Enabled: true, Schedule: b.Schedule, Bucket: b.S3.Bucket, Region: b.S3.Region, Endpoint: b.S3.Endpoint, Prefix: prefix, PathStyle: b.S3.PathStyle, Compression: b.Compression, AgeRecipients: b.AgeRecipients, Cluster: c.Metadata.Name}
}

type buildsVars struct {
	Enabled bool   `json:"enabled"`
	IP      string `json:"ip"`
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

var metallbDefaults = map[string]any{
	"frrk8s":     map[string]any{"enabled": false},
	"speaker":    map[string]any{"frr": map[string]any{"enabled": false}, "resources": requests("20m", "64Mi")},
	"controller": map[string]any{"resources": requests("20m", "64Mi")},
}

var traefikDefaults = map[string]any{
	"deployment":   map[string]any{"replicas": 1},
	"resources":    requests("50m", "64Mi"),
	"ingressClass": map[string]any{"enabled": true, "isDefaultClass": true},
	"providers": map[string]any{
		"kubernetesIngress":      map[string]any{"enabled": true},
		"kubernetesIngressNGINX": map[string]any{"enabled": true, "publishService": map[string]any{"enabled": true}},
		"kubernetesGateway":      map[string]any{"enabled": true},
	},
}

var metricsDefaults = map[string]any{
	"resources": requests("20m", "48Mi"),
}

func requests(cpu, mem string) map[string]any {
	return map[string]any{"requests": map[string]any{"cpu": cpu, "memory": mem}}
}

func merged(defaults, over map[string]any) map[string]any {
	out := maps.Clone(defaults)
	if out == nil {
		out = map[string]any{}
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

var ChartVersions = map[string]string{
	"metallb":        "0.16.1",
	"traefik":        "41.6.1",
	"metrics-server": "3.14.0",
	"cert-manager":   "v1.21.2",
	"flux":           "2.19.1",
	"longhorn":       "1.10.1",
	"talos-backup":   "v0.1.0-beta.2",
}

func chartVersionVars() map[string]string {
	out := make(map[string]string, len(ChartVersions))
	for addon, v := range ChartVersions {
		out[strings.ReplaceAll(addon, "-", "_")] = v
	}
	return out
}

func ingressPin(p config.Platform, recorded string) string {
	ip, err := netip.ParseAddr(recorded)
	if !p.MetalLB.Enabled || err != nil {
		return ""
	}
	lo, hi, err := config.ParseIPRange(p.MetalLB.Range)
	if err != nil || !config.InRange(ip, lo, hi) {
		return ""
	}
	return ip.String()
}

func Vars(c *config.Cluster, kubeconfigPath, ingressIP string) map[string]any {
	p := c.Spec.Platform
	return map[string]any{
		"kubeconfig":       kubeconfigPath,
		"metallb":          metallbVars{Enabled: p.MetalLB.Enabled, Range: p.MetalLB.Range, Values: merged(metallbDefaults, p.MetalLB.Values)},
		"traefik":          addonVars{p.Traefik.Enabled, merged(traefikDefaults, p.Traefik.Values)},
		"ingress_ip_pin":   ingressPin(p, ingressIP),
		"gvisor":           map[string]bool{"enabled": p.GVisor.Enabled},
		"metrics_server":   addonVars{p.MetricsServer.Enabled, merged(metricsDefaults, p.MetricsServer.Values)},
		"cert_manager":     addonVars{p.CertManager.Enabled, vals(p.CertManager.Values)},
		"builds":           buildsVars{Enabled: p.Builds.Enabled, IP: c.RegistryIP()},
		"flux":             fluxVars{Enabled: p.Flux.Enabled, Values: vals(p.Flux.Values), Repository: p.Flux.Repository},
		"longhorn":         longhornVars{Enabled: p.Longhorn.Enabled, Values: vals(p.Longhorn.Values), Replicas: c.LonghornReplicas()},
		"oidc_admin_group": c.Spec.Auth.AdminGroupSubject(),
		"backup":           backupVarsOf(c),
		"chart_versions":   chartVersionVars(),
	}
}
