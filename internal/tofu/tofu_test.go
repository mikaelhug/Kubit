package tofu_test

import (
	"encoding/json"
	"os"
	"path/filepath"
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
    ingressNginx: { enabled: true }
    gvisor: { enabled: true }
    metricsServer: { enabled: false }
    certManager: { enabled: true, values: { replicaCount: 2, prometheus: { enabled: false } } }
    argocd: { enabled: false }
`

func TestRenderWritesModuleAndVars(t *testing.T) {
	c, err := config.Parse([]byte(decl))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	// A pre-existing state file must survive re-rendering.
	if err := os.WriteFile(filepath.Join(dir, "terraform.tfstate"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := tofu.Render(dir, c, "/x/kubeconfig"); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"versions.tf", "variables.tf", "metallb.tf", "ingress-nginx.tf", "gvisor.tf", "metrics-server.tf", "cert-manager.tf", "argocd.tf", "outputs.tf", "terraform.tfvars.json", "terraform.tfstate"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s missing", f)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "terraform.tfvars.json"))
	var vars map[string]json.RawMessage
	if err := json.Unmarshal(raw, &vars); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"kubeconfig":     `"/x/kubeconfig"`,
		"metallb":        `{"enabled":true,"range":"10.0.0.200-10.0.0.210","values":{"controller":{"resources":{"requests":{"cpu":"20m","memory":"64Mi"}}},"frrk8s":{"enabled":false},"speaker":{"frr":{"enabled":false},"resources":{"requests":{"cpu":"20m","memory":"64Mi"}}}}}`,
		"ingress_nginx":  `{"enabled":true,"values":{"controller":{"replicaCount":1,"resources":{"requests":{"cpu":"50m","memory":"128Mi"}}}}}`,
		"gvisor":         `{"enabled":true,"values":{}}`,
		"metrics_server": `{"enabled":false,"values":{"resources":{"requests":{"cpu":"20m","memory":"48Mi"}}}}`,
		"cert_manager":   `{"enabled":true,"values":{"prometheus":{"enabled":false},"replicaCount":2}}`,
		"argocd":         `{"enabled":false,"values":{}}`,
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
	v := tofu.Vars(c, "/x/kubeconfig")["metallb"].(tofu.MetallbVars).Values
	if v["frrk8s"].(map[string]any)["enabled"] != true {
		t.Errorf("user value must win: %v", v["frrk8s"])
	}
	sp := v["speaker"].(map[string]any)
	if sp["logLevel"] != "debug" || sp["frr"].(map[string]any)["enabled"] != false || sp["resources"] == nil {
		t.Errorf("sibling defaults must survive an override: %v", sp)
	}
	plain := tofu.Vars(&config.Cluster{}, "/x/kubeconfig")["metallb"].(tofu.MetallbVars).Values
	if plain["frrk8s"].(map[string]any)["enabled"] != false {
		t.Error("defaults were mutated by the merge")
	}
}
