package config

import (
	"fmt"

	talosconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/configpatcher"
	"go.yaml.in/yaml/v4"
)

type sourcedPatch struct {
	path  string
	patch map[string]any
}

func nodePatches(c *Cluster, n Node) []sourcedPatch {
	return append(patchSet("", c.Spec.Patches), patchSet(n.Hostname+".", n.Patches)...)
}

func patchSet(prefix string, patches []map[string]any) []sourcedPatch {
	out := make([]sourcedPatch, 0, len(patches))
	for i, p := range patches {
		out = append(out, sourcedPatch{path: fmt.Sprintf("%spatches[%d]", prefix, i), patch: p})
	}
	return out
}

func loadPatch(p map[string]any) (configpatcher.Patch, error) {
	b, err := yaml.Marshal(p)
	if err != nil {
		return nil, err
	}
	return configpatcher.LoadPatch(b)
}

func applyPatches(cfg talosconfig.Provider, sourced []sourcedPatch) (talosconfig.Provider, error) {
	if len(sourced) == 0 {
		return cfg, nil
	}
	patches := make([]configpatcher.Patch, 0, len(sourced))
	for _, s := range sourced {
		p, err := loadPatch(s.patch)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.path, err)
		}
		patches = append(patches, p)
	}
	out, err := configpatcher.Apply(configpatcher.WithConfig(cfg), patches)
	if err != nil {
		return nil, fmt.Errorf("patches: %w", err)
	}
	return out.Config()
}

func (c *Cluster) validatePatches() []error {
	all := patchSet("", c.Spec.Patches)
	for _, n := range c.Spec.Nodes {
		all = append(all, patchSet(n.Hostname+".", n.Patches)...)
	}
	var errs []error
	for _, s := range all {
		if _, err := loadPatch(s.patch); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.path, err))
		}
	}
	return errs
}
