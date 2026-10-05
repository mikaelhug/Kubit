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

func TestRenderWritesModuleAndVars(t *testing.T) {
	c, err := config.Parse([]byte(decl))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "terraform.tfstate"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ingress-nginx.tf"), []byte(`resource "helm_release" "ingress_nginx" {}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := tofu.Render(dir, c, "/x/kubeconfig", ""); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"versions.tf", "variables.tf", "metallb.tf", "traefik.tf", "gvisor.tf", "metrics-server.tf", "cert-manager.tf", "flux.tf", "builds.tf", "outputs.tf", "terraform.tfvars.json", "terraform.tfstate"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s missing", f)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "ingress-nginx.tf")); !os.IsNotExist(err) {
		t.Errorf("a template that no longer exists must be removed: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "terraform.tfvars.json"))
	var vars map[string]json.RawMessage
	if err := json.Unmarshal(raw, &vars); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"kubeconfig":     `"/x/kubeconfig"`,
		"builds":         `{"enabled":false,"ip":"10.96.0.50"}`,
		"metallb":        `{"enabled":true,"range":"10.0.0.200-10.0.0.210","values":{"controller":{"resources":{"requests":{"cpu":"20m","memory":"64Mi"}}},"frrk8s":{"enabled":false},"speaker":{"frr":{"enabled":false},"resources":{"requests":{"cpu":"20m","memory":"64Mi"}}}}}`,
		"traefik":        `{"enabled":true,"values":{"deployment":{"replicas":1},"ingressClass":{"enabled":true,"isDefaultClass":true},"providers":{"kubernetesGateway":{"enabled":true},"kubernetesIngress":{"enabled":true},"kubernetesIngressNGINX":{"enabled":true,"publishService":{"enabled":true}}},"resources":{"requests":{"cpu":"50m","memory":"64Mi"}}}}`,
		"ingress_ip_pin": `""`,
		"gvisor":         `{"enabled":true,"values":{}}`,
		"metrics_server": `{"enabled":false,"values":{"resources":{"requests":{"cpu":"20m","memory":"48Mi"}}}}`,
		"cert_manager":   `{"enabled":true,"values":{"prometheus":{"enabled":false},"replicaCount":2}}`,
		"flux":           `{"enabled":true,"values":{},"repository":{"url":"https://github.com/mikaelhug/kubit-apps.git","branch":"main","path":"./apps","interval":"5m"}}`,
		"chart_versions": `{"cert_manager":"v1.21.2","flux":"2.19.1","gateway_api":"v1.6.1","longhorn":"1.10.1","metallb":"0.16.1","metrics_server":"3.14.0","talos_backup":"v0.1.0-beta.2","traefik":"41.6.1"}`,
	}
	for k, w := range want {
		var got, exp any
		_ = json.Unmarshal(vars[k], &got)
		_ = json.Unmarshal([]byte(w), &exp)
		if gb, _ := json.Marshal(got); string(gb) != mustCompact(w) {
			t.Errorf("%s = %s, want %s", k, gb, w)
		}
	}
}

func TestFluxDecryptsWithKubitsKey(t *testing.T) {
	c, err := config.Parse([]byte(decl))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := tofu.Render(dir, c, "/x/kubeconfig", ""); err != nil {
		t.Fatal(err)
	}
	tf, _ := os.ReadFile(filepath.Join(dir, "flux.tf"))
	if !strings.Contains(string(tf), `secretRef = { name = "`+tofu.SOPSSecret+`" }`) || !strings.Contains(string(tf), `namespace = "`+tofu.SOPSNamespace+`"`) {
		t.Errorf("flux.tf does not read %s/%s:\n%s", tofu.SOPSNamespace, tofu.SOPSSecret, tf)
	}
	for _, r := range []string{"helm_release.traefik", "kubectl_manifest.traefik_nginx_class", "helm_release.cert_manager", "helm_release.longhorn", "kubectl_manifest.metallb_l2"} {
		if !strings.Contains(string(tf), "    "+r+",\n") {
			t.Errorf("the first sync must wait for %s", r)
		}
	}
}

func mustCompact(s string) string {
	var v any
	_ = json.Unmarshal([]byte(s), &v)
	b, _ := json.Marshal(v)
	return string(b)
}

func TestSummary(t *testing.T) {
	if !(tofu.Summary{}).Empty() || (tofu.Summary{Add: 1}).Empty() {
		t.Error("Empty")
	}
	if s := (tofu.Summary{Add: 2, Change: 1, Remove: 0}).String(); s != "2 to add, 1 to change, 0 to destroy" {
		t.Error(s)
	}
}

func TestUserValuesOverrideAddonDefaults(t *testing.T) {
	c := &config.Cluster{}
	c.Spec.Platform.MetalLB = config.MetalLB{Enabled: true, Range: "10.0.0.200-10.0.0.201", Values: map[string]any{"frrk8s": map[string]any{"enabled": true}, "speaker": map[string]any{"logLevel": "debug"}}}
	v := tofu.Vars(c, "/x/kubeconfig", "")["metallb"].(tofu.MetallbVars).Values
	if v["frrk8s"].(map[string]any)["enabled"] != true {
		t.Errorf("user value must win: %v", v["frrk8s"])
	}
	sp := v["speaker"].(map[string]any)
	if sp["logLevel"] != "debug" || sp["frr"].(map[string]any)["enabled"] != false || sp["resources"] == nil {
		t.Errorf("sibling defaults must survive an override: %v", sp)
	}
	plain := tofu.Vars(&config.Cluster{}, "/x/kubeconfig", "")["metallb"].(tofu.MetallbVars).Values
	if plain["frrk8s"].(map[string]any)["enabled"] != false {
		t.Error("defaults were mutated by the merge")
	}
}

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

func TestVarsPinIngressIPOnlyInsideRange(t *testing.T) {
	c := &config.Cluster{}
	c.Spec.Platform.MetalLB = config.MetalLB{Enabled: true, Range: "10.0.0.200-10.0.0.210"}
	c.Spec.Platform.Traefik.Enabled = true
	for recorded, want := range map[string]string{
		"10.0.0.200": "10.0.0.200",
		"10.0.0.210": "10.0.0.210",
		"10.0.0.199": "",
		"10.0.0.211": "",
		"fd00::1":    "",
		"":           "",
		"garbage":    "",
	} {
		if got := tofu.Vars(c, "/x/kubeconfig", recorded)["ingress_ip_pin"]; got != want {
			t.Errorf("pin for %q = %q, want %q", recorded, got, want)
		}
	}
	c.Spec.Platform.MetalLB.Enabled = false
	if got := tofu.Vars(c, "/x/kubeconfig", "10.0.0.200")["ingress_ip_pin"]; got != "" {
		t.Errorf("without MetalLB there is nothing to pin: %q", got)
	}
}

func TestRenderKeepsThePinWhenNoAddressIsRecorded(t *testing.T) {
	c, err := config.Parse([]byte(decl))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	pin := func() string {
		raw, _ := os.ReadFile(filepath.Join(dir, "terraform.tfvars.json"))
		var v struct {
			Pin string `json:"ingress_ip_pin"`
		}
		_ = json.Unmarshal(raw, &v)
		return v.Pin
	}
	if err := tofu.Render(dir, c, "/x/kubeconfig", "10.0.0.205"); err != nil || pin() != "10.0.0.205" {
		t.Fatalf("first render: pin %q, %v", pin(), err)
	}
	if err := tofu.Render(dir, c, "/x/kubeconfig", ""); err != nil || pin() != "10.0.0.205" {
		t.Errorf("a failed apply clears the recorded address; the pin must survive it: %q, %v", pin(), err)
	}
	c.Spec.Platform.MetalLB.Range = "10.0.0.100-10.0.0.110"
	if err := tofu.Render(dir, c, "/x/kubeconfig", ""); err != nil || pin() != "" {
		t.Errorf("a pin outside the new range must be dropped: %q, %v", pin(), err)
	}
}

func TestTraefikKeepsTheNginxClassAndPinsItsAddress(t *testing.T) {
	c, err := config.Parse([]byte(decl))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := tofu.Render(dir, c, "/x/kubeconfig", ""); err != nil {
		t.Fatal(err)
	}
	tf, _ := os.ReadFile(filepath.Join(dir, "traefik.tf"))
	for _, s := range []string{
		`controller = "k8s.io/ingress-nginx"`,
		`metadata   = { name = "nginx" }`,
		`service.annotations.metallb\\.io/loadBalancerIPs`,
		`depends_on = [kubectl_manifest.metallb_l2, kubectl_manifest.traefik_gateway_api]`,
		`apply_only        = true`,
	} {
		if !strings.Contains(string(tf), s) {
			t.Errorf("traefik.tf lacks %s", s)
		}
	}
	out, _ := os.ReadFile(filepath.Join(dir, "outputs.tf"))
	if !strings.Contains(string(out), `output "ingress_ip"`) || !strings.Contains(string(out), "data.kubernetes_service_v1.traefik[0]") {
		t.Errorf("ingress_ip must read the Traefik Service:\n%s", out)
	}
}

func TestGatewayAPICRDsShipWithKubit(t *testing.T) {
	c, err := config.Parse([]byte(decl))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	stale := filepath.Join(dir, "gateway-api-v1.0.0.yaml")
	if err := os.WriteFile(stale, []byte("kind: CustomResourceDefinition\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := tofu.Render(dir, c, "/x/kubeconfig", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("a CRD bundle that no longer ships must be removed: %v", err)
	}
	name := "gateway-api-" + tofu.ChartVersions["gateway-api"] + ".yaml"
	crds, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("%s is not rendered: %v", name, err)
	}
	if n := strings.Count(string(crds), "\nkind: CustomResourceDefinition\n"); n < 10 || !strings.Contains(string(crds), "bundle-version: "+tofu.ChartVersions["gateway-api"]) {
		t.Errorf("%s: %d CRDs", name, n)
	}
	tf, _ := os.ReadFile(filepath.Join(dir, "traefik.tf"))
	if !strings.Contains(string(tf), `file("${path.module}/gateway-api-${var.chart_versions.gateway_api}.yaml")`) {
		t.Errorf("traefik.tf must read the shipped CRDs:\n%s", tf)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".tf" {
			continue
		}
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		if strings.Contains(string(b), `data "http"`) || strings.Contains(string(b), "hashicorp/http") {
			t.Errorf("%s fetches from the network at plan time", e.Name())
		}
	}
}
