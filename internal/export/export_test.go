package export_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestWriteDropsMachineConfigsOfRemovedNodes(t *testing.T) {
	c, err := config.Parse([]byte(decl))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	in := export.Input{Cluster: c, ClusterYAML: []byte("c"), SecretsYAML: []byte("s"), Talosconfig: []byte("tc"),
		MachineConfigs: map[string][]byte{"cp-01": []byte("a"), "w-01": []byte("b"), "w-02": []byte("c")}}
	if err := export.Write(context.Background(), dir, in); err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(dir, "machineconfigs", "patch.yaml")
	if err := os.WriteFile(mine, []byte("user"), 0o600); err != nil {
		t.Fatal(err)
	}
	delete(in.MachineConfigs, "w-02")
	if err := export.Write(context.Background(), dir, in); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"machineconfigs", "infra/talos/machineconfigs"} {
		if _, err := os.Stat(filepath.Join(dir, sub, "w-02.yaml")); !os.IsNotExist(err) {
			t.Errorf("%s/w-02.yaml survived the node's removal: %v", sub, err)
		}
		if _, err := os.Stat(filepath.Join(dir, sub, "w-01.yaml")); err != nil {
			t.Errorf("%s/w-01.yaml: %v", sub, err)
		}
	}
	if b, err := os.ReadFile(mine); err != nil || string(b) != "user" {
		t.Errorf("a file Kubit did not write must survive an export: %q %v", b, err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, ".kubit-export.json"))
	if !strings.Contains(string(raw), "w-01.yaml") || strings.Contains(string(raw), "w-02.yaml") {
		t.Errorf("manifest lists the current export only:\n%s", raw)
	}
	tf, _ := os.ReadFile(filepath.Join(dir, "infra/talos/versions.tf"))
	if !strings.Contains(string(tf), "< 1.0") {
		t.Errorf("provider major not pinned:\n%s", tf)
	}
}
