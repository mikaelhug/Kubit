// Package export writes a cluster's Talos layer out of Kubit: native artefacts usable
// with talosctl/kubectl directly, plus an OpenTofu root using the siderolabs/talos
// provider. It is a one-way snapshot; Kubit never executes it.
package export

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/mikael/kubit/internal/config"
)

//go:embed templates
var templates embed.FS

// Input is everything the export needs; the cluster package assembles it from the store.
type Input struct {
	Cluster        *config.Cluster
	ClusterYAML    []byte
	SecretsYAML    []byte
	Talosconfig    []byte
	Kubeconfig     []byte            // may be nil before bootstrap
	MachineConfigs map[string][]byte // hostname → applied config
}

// Write lays out dir:
//
//	cluster.yaml  secrets.yaml  talosconfig  kubeconfig
//	machineconfigs/<hostname>.yaml
//	infra/talos/{versions,variables,main}.tf  terraform.tfvars.json  README.md
//	infra/talos/secrets.yaml  infra/talos/machineconfigs/  (copies the provider reads)
func Write(_ context.Context, dir string, in Input) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	files := map[string][]byte{
		"cluster.yaml": in.ClusterYAML,
		"secrets.yaml": in.SecretsYAML,
		"talosconfig":  in.Talosconfig,
	}
	if in.Kubeconfig != nil {
		files["kubeconfig"] = in.Kubeconfig
	}
	for host, cfg := range in.MachineConfigs {
		files[filepath.Join("machineconfigs", host+".yaml")] = cfg
		files[filepath.Join("infra", "talos", "machineconfigs", host+".yaml")] = cfg
	}
	files[filepath.Join("infra", "talos", "secrets.yaml")] = in.SecretsYAML

	entries, err := fs.ReadDir(templates, "templates")
	if err != nil {
		return err
	}
	for _, e := range entries {
		b, err := templates.ReadFile("templates/" + e.Name())
		if err != nil {
			return err
		}
		files[filepath.Join("infra", "talos", e.Name())] = b
	}
	vars, err := json.MarshalIndent(Vars(in.Cluster), "", "  ")
	if err != nil {
		return err
	}
	files[filepath.Join("infra", "talos", "terraform.tfvars.json")] = append(vars, '\n')
	files[filepath.Join("infra", "talos", "README.md")] = []byte(readme)

	for rel, b := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(p, b, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}
	return nil
}

type nodeVar struct {
	IP   string `json:"ip"`
	Role string `json:"role"`
}

func Vars(c *config.Cluster) map[string]any {
	nodes := map[string]nodeVar{}
	for _, n := range c.Spec.Nodes {
		nodes[n.Hostname] = nodeVar{IP: n.IP, Role: string(n.Role)}
	}
	return map[string]any{
		"cluster_name":       c.Metadata.Name,
		"cluster_endpoint":   c.Spec.ControlPlane.Endpoint,
		"talos_version":      c.Spec.TalosVersion,
		"kubernetes_version": c.Spec.KubernetesVersion,
		"bootstrap_node":     c.ControlPlanes()[0].IP,
		"nodes":              nodes,
	}
}

const readme = `# Talos layer export

This directory reproduces the cluster's Talos layer with the siderolabs/talos OpenTofu
provider. It is a snapshot written by Kubit; Kubit does not run it.

    tofu init
    tofu plan

The plan imports the cluster PKI from secrets.yaml (talos_machine_secrets supports
import) and shows talos_machine_configuration_apply and talos_cluster_kubeconfig as new
resources, which the provider cannot import. Applying re-sends the identical machine
configs, a no-op on the nodes, and reads the kubeconfig. etcd bootstrap is left out
(bootstrap = false): the provider fails with AlreadyExists on a bootstrapped node. Set
bootstrap = true only to recreate the cluster on wiped nodes. Check the plan before
applying to a cluster you care about.

Native artefacts one level up (secrets.yaml, talosconfig, kubeconfig, machineconfigs/)
work directly with talosctl and kubectl:

    talosctl --talosconfig ../../talosconfig -n <ip> version
`
