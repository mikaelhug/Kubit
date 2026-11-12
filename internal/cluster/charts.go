package cluster

import (
	"context"
	"fmt"
	"sort"

	"github.com/mikaelhug/kubit/internal/config"
	"github.com/mikaelhug/kubit/internal/tofu"
)

type chartTargets struct {
	versions map[string]string
	rows     map[string]string
	problems []string
}

func (m *Manager) chartTargets(ctx context.Context, c *config.Cluster, kubeconfig []byte) (chartTargets, error) {
	t := chartTargets{versions: tofu.NewestVersions(), rows: map[string]string{}}
	kc, err := m.KubeClientFor(c.Metadata.Name, kubeconfig)
	if err != nil {
		return t, err
	}
	sv, err := kc.Discovery().ServerVersion()
	if err != nil {
		return t, fmt.Errorf("Kubernetes version: %w", err)
	}
	names := make([]string, 0, len(tofu.Charts))
	for name := range tofu.Charts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		meta, ok := addonByTofu(name)
		if !ok || !addonEnabled(c, meta.key) {
			continue
		}
		installed := ""
		rel, err := kc.HelmRelease(ctx, meta.namespace, meta.release)
		if err != nil {
			return t, fmt.Errorf("%s release: %w", name, err)
		}
		if rel != nil {
			installed = rel.ChartVersion
		}
		target, err := tofu.Charts[name].Target(name, installed)
		if err != nil {
			t.problems = append(t.problems, err.Error())
			continue
		}
		t.versions[name] = target.Step.Version
		if err := target.Step.GatewayAPIFits(name); err != nil {
			t.problems = append(t.problems, err.Error())
		}
		if installed == target.Step.Version {
			continue
		}
		if err := target.Step.Fits(name, sv.GitVersion); err != nil {
			t.problems = append(t.problems, err.Error())
		}
		row := target.Step.Version
		if installed != "" {
			row = installed + " → " + row
		}
		if target.Of > 1 {
			row += fmt.Sprintf(" (%d of %d)", target.N, target.Of)
		}
		t.rows[name] = row
	}
	return t, nil
}

func addonByTofu(name string) (struct{ key, tofu, namespace, release string }, bool) {
	for _, a := range addonMeta {
		if a.tofu == name && a.release != "" {
			return a, true
		}
	}
	return struct{ key, tofu, namespace, release string }{}, false
}
