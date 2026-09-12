package export_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/export"
)

const decl = `
apiVersion: kubit.dev/v1
kind: Cluster
metadata: { name: t }
spec:
  nodes:
    - { hostname: cp-01, ip: 10.0.0.1, role: controlplane, installDisk: { path: /dev/sda } }
    - { hostname: w-01, ip: 10.0.0.2, role: worker, installDisk: { path: /dev/sda } }
`

func TestWriteLayout(t *testing.T) {
	c, err := config.Parse([]byte(decl))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	err = export.Write(context.Background(), dir, export.Input{
		Cluster: c, ClusterYAML: []byte("c"), SecretsYAML: []byte("s"), Talosconfig: []byte("tc"), Kubeconfig: []byte("kc"),
		MachineConfigs: map[string][]byte{"cp-01": []byte("a"), "w-01": []byte("b")},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"cluster.yaml", "secrets.yaml", "talosconfig", "kubeconfig", "machineconfigs/cp-01.yaml", "machineconfigs/w-01.yaml",
		"infra/talos/versions.tf", "infra/talos/variables.tf", "infra/talos/main.tf", "infra/talos/terraform.tfvars.json", "infra/talos/README.md",
		"infra/talos/secrets.yaml", "infra/talos/machineconfigs/cp-01.yaml"} {
		st, err := os.Stat(filepath.Join(dir, f))
		if err != nil {
			t.Errorf("%s missing", f)
			continue
		}
		if st.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %o", f, st.Mode().Perm())
		}
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "infra/talos/terraform.tfvars.json"))
	var vars struct {
		Endpoint  string                       `json:"cluster_endpoint"`
		Bootstrap string                       `json:"bootstrap_node"`
		Nodes     map[string]map[string]string `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &vars); err != nil {
		t.Fatal(err)
	}
	if vars.Endpoint != "https://10.0.0.1:6443" || vars.Bootstrap != "10.0.0.1" || vars.Nodes["w-01"]["role"] != "worker" {
		t.Errorf("vars: %+v", vars)
	}
}
