package tofu_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/tofu"
)

const decl = `
apiVersion: kubit.dev/v1
kind: Cluster
metadata: { name: t }
spec:
  nodes:
    - { hostname: cp-01, ip: 10.0.0.1, role: controlplane, installDisk: { path: /dev/sda } }
  platform:
    metallb: { enabled: true, range: 10.0.0.200-10.0.0.210 }
    traefik: { enabled: true }
    gvisor: { enabled: true }
    metricsServer: { enabled: false }
    certManager: { enabled: true, values: { replicaCount: 2, prometheus: { enabled: false } } }
    flux: { enabled: true, repository: { url: https://github.com/mikaelhug/kubit-apps.git, path: ./apps } }
`

func TestEveryChartVersionTheTemplatesReadIsRendered(t *testing.T) {
	c, err := config.Parse([]byte(decl))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := tofu.Render(dir, c, "/x/kubeconfig", ""); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "terraform.tfvars.json"))
	var vars struct {
		Charts map[string]string `json:"chart_versions"`
	}
	if err := json.Unmarshal(raw, &vars); err != nil {
		t.Fatal(err)
	}
	ref := regexp.MustCompile(`var\.chart_versions\.([a-z_]+)`)
	chartVar := regexp.MustCompile(`variable "chart_versions" \{[^}]*\}`)
	entries, _ := os.ReadDir(dir)
	seen := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".tf" {
			continue
		}
		tf, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		if block := chartVar.FindString(string(tf)); strings.Contains(block, "default") {
			t.Errorf("%s: chart versions come from terraform.tfvars.json, not a variable default", e.Name())
		}
		for _, m := range ref.FindAllStringSubmatch(string(tf), -1) {
			seen++
			if vars.Charts[m[1]] == "" {
				t.Errorf("%s reads chart_versions.%s, which is not rendered", e.Name(), m[1])
			}
		}
	}
	if seen != len(tofu.ChartVersions) {
		t.Errorf("templates read %d chart versions, Go pins %d", seen, len(tofu.ChartVersions))
	}
}

func TestGatewayAPICRDsSurviveHelmAndFitTheStateSecret(t *testing.T) {
	c, err := config.Parse([]byte(decl))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := tofu.Render(dir, c, "/x/kubeconfig", ""); err != nil {
		t.Fatal(err)
	}
	crds, err := os.ReadFile(filepath.Join(dir, "charts", "gateway-api", "templates", "crds.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	docs := strings.Count(string(crds), "\nkind: CustomResourceDefinition\n")
	if docs == 0 {
		t.Fatal("no CRDs in the chart")
	}
	if n := strings.Count(string(crds), "    helm.sh/resource-policy: keep\n"); n != docs {
		t.Errorf("%d of %d CRDs survive a Helm uninstall; deleting one deletes every Gateway and route", n, docs)
	}
	if strings.Contains(string(crds), "{{") {
		t.Error("the CRDs are rendered by Helm; template braces would be evaluated")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".tf" {
			continue
		}
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		if strings.Contains(string(b), "kubectl_file_documents") {
			t.Errorf("%s puts a manifest bundle into the state, which must fit a Secret", e.Name())
		}
	}
}

func TestLockFileHasProviderHashesForEveryPlatform(t *testing.T) {
	c, err := config.Parse([]byte(decl))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := tofu.Render(dir, c, "/x/kubeconfig", ""); err != nil {
		t.Fatal(err)
	}
	versions, _ := os.ReadFile(filepath.Join(dir, "versions.tf"))
	lock, err := os.ReadFile(filepath.Join(dir, ".terraform.lock.hcl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range regexp.MustCompile(`source\s*=\s*"([^"]+)"`).FindAllStringSubmatch(string(versions), -1) {
		block := regexp.MustCompile(`(?s)provider "registry\.opentofu\.org/` + regexp.QuoteMeta(m[1]) + `" \{.*?\n\}`).FindString(string(lock))
		if block == "" {
			t.Errorf("%s is not in the lock file", m[1])
			continue
		}
		if n := strings.Count(block, `"h1:`); n < 4 {
			t.Errorf("%s: %d platform hashes; darwin and linux on amd64 and arm64 need 4", m[1], n)
		}
	}
}
