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
	Enabled         bool                   `json:"enabled"`
	Range           string                 `json:"range,omitempty"`
	Repository      *config.FluxRepository `json:"repository,omitempty"`
	ImageAutomation *bool                  `json:"imageAutomation,omitempty"`
	Replicas        *int                   `json:"replicas,omitempty"`
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
		if key == "longhorn" && e.Replicas != nil {
			if *e.Replicas > 0 {
				setKey(addon, "replicas", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprint(*e.Replicas)})
			} else {
				deleteKey(addon, "replicas")
			}
		}
		if key == "flux" && e.ImageAutomation != nil {
			if *e.ImageAutomation {
				setKey(addon, "imageAutomation", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})
			} else {
				deleteKey(addon, "imageAutomation")
			}
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
			for _, item := range nodeItems(spec) {
				if v := yamlx.Value(item, "talosVersion"); v != nil && !config.TalosBefore(talos, v.Value) {
					deleteKey(item, "talosVersion")
				}
			}
		}
		if kubernetes != "" {
			setKey(spec, "kubernetesVersion", &yaml.Node{Kind: yaml.ScalarNode, Value: kubernetes})
		}
		return nil
	})
}

func SetNodeTalos(dir, hash string, hostnames []string, version string) error {
	if err := requireHash(hash); err != nil {
		return err
	}
	return editSpec(dir, hash, func(spec *yaml.Node) error {
		cluster := config.DefaultTalosVersion()
		if v := yamlx.Value(spec, "talosVersion"); v != nil && v.Value != "" {
			cluster = v.Value
		}
		for _, h := range hostnames {
			if err := withNode(spec, h, func(item *yaml.Node) error {
				if version == cluster {
					deleteKey(item, "talosVersion")
				} else {
					setKey(item, "talosVersion", &yaml.Node{Kind: yaml.ScalarNode, Value: version})
				}
				return nil
			}); err != nil {
				return err
			}
		}
		items := nodeItems(spec)
		shared := ""
		for _, item := range items {
			v := yamlx.Value(item, "talosVersion")
			if v == nil || (shared != "" && v.Value != shared) {
				return nil
			}
			shared = v.Value
		}
		if shared == "" {
			return nil
		}
		setKey(spec, "talosVersion", &yaml.Node{Kind: yaml.ScalarNode, Value: shared})
		for _, item := range items {
			deleteKey(item, "talosVersion")
		}
		return nil
	})
}

func nodeItems(spec *yaml.Node) []*yaml.Node {
	if nodes := yamlx.Value(spec, "nodes"); nodes != nil && nodes.Kind == yaml.SequenceNode {
		return nodes.Content
	}
	return nil
}

func mapping(m *yaml.Node, key string) *yaml.Node {
	if v := yamlx.Value(m, key); v != nil && v.Kind == yaml.MappingNode {
		return v
	}
	v := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	setKey(m, key, v)
	return v
}

func SetNodeSystemDisk(dir, hash, hostname string, on bool) error {
	if err := requireHash(hash); err != nil {
		return err
	}
	return editSpec(dir, hash, func(spec *yaml.Node) error {
		def := false
		if v := yamlx.Value(yamlx.Value(spec, "storage"), "systemDisk"); v != nil {
			if err := v.Decode(&def); err != nil {
				return err
			}
		}
		return withNode(spec, hostname, func(item *yaml.Node) error {
			if on == def {
				deleteKey(item, "storage")
				return nil
			}
			setKey(mapping(item, "storage"), "systemDisk", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: fmt.Sprint(on)})
			return nil
		})
	})
}
