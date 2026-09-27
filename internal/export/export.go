package export

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/fsx"
)

//go:embed templates
var templates embed.FS

type Input struct {
	Cluster        *config.Cluster
	ClusterYAML    []byte
	SecretsYAML    []byte
	Talosconfig    []byte
	Kubeconfig     []byte
	MachineConfigs map[string][]byte
}

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

	previous, err := readManifest(dir)
	if err != nil {
		return err
	}
	for rel, b := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(p, b, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}
	for _, rel := range previous {
		if _, current := files[rel]; current || !filepath.IsLocal(rel) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, rel)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return writeManifest(dir, slices.Sorted(maps.Keys(files)))
}

const manifestFile = ".kubit-export.json"

type manifest struct {
	Files []string `json:"files"`
}

func readManifest(dir string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(dir, manifestFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m manifest
	_ = json.Unmarshal(b, &m)
	return m.Files, nil
}

func writeManifest(dir string, files []string) error {
	b, err := json.MarshalIndent(manifest{Files: files}, "", "  ")
	if err != nil {
		return err
	}
	return fsx.WriteFile(filepath.Join(dir, manifestFile), append(b, '\n'), 0o600)
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
