package repo

import (
	"fmt"
	"slices"

	"github.com/mikaelhug/kubit/internal/config"
	"github.com/mikaelhug/kubit/internal/yamlx"
	"go.yaml.in/yaml/v4"
)

var AddonKeys = []string{"metallb", "traefik", "gvisor", "metricsServer", "certManager", "flux", "longhorn", "builds"}

type AddonEdit struct {
	Enabled    bool                   `json:"enabled"`
	Range      string                 `json:"range,omitempty"`
	Repository *config.FluxRepository `json:"repository,omitempty"`
}

func SetAddon(dir, hash, key string, e AddonEdit) error {
	if !slices.Contains(AddonKeys, key) {
		return fmt.Errorf("unknown add-on %q", key)
	}
	if err := requireHash(hash); err != nil {
		return err
	}
	return editSpec(dir, hash, func(spec *yaml.Node) error {
		addon := mapping(mapping(spec, "platform"), key)
		setKey(addon, "enabled", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: fmt.Sprint(e.Enabled)})
		if key == "metallb" && e.Range != "" {
			setKey(addon, "range", &yaml.Node{Kind: yaml.ScalarNode, Value: e.Range})
		}
		if key == "flux" && e.Repository != nil {
			if e.Repository.URL == "" {
				deleteKey(addon, "repository")
				return nil
			}
			var v yaml.Node
			if err := v.Encode(e.Repository); err != nil {
				return err
			}
			setKey(addon, "repository", &v)
		}
		return nil
	})
}

func SetVersions(dir, hash, talos, kubernetes string) error {
	if err := requireHash(hash); err != nil {
		return err
	}
	return editSpec(dir, hash, func(spec *yaml.Node) error {
		if talos != "" {
			setKey(spec, "talosVersion", &yaml.Node{Kind: yaml.ScalarNode, Value: talos})
		}
		if kubernetes != "" {
			setKey(spec, "kubernetesVersion", &yaml.Node{Kind: yaml.ScalarNode, Value: kubernetes})
		}
		return nil
	})
}

func mapping(m *yaml.Node, key string) *yaml.Node {
	if v := yamlx.Value(m, key); v != nil && v.Kind == yaml.MappingNode {
		return v
	}
	v := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	setKey(m, key, v)
	return v
}
