package tofu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type Group struct {
	Addon   string
	Actions string
	Diff    string
}

type showPlan struct {
	ResourceChanges []struct {
		Address string `json:"address"`
		Mode    string `json:"mode"`
		Type    string `json:"type"`
		Change  change `json:"change"`
	} `json:"resource_changes"`
}

func (r *Runner) ShowPlan(ctx context.Context) ([]Group, error) {
	out, err := r.output(ctx, "show", "-json", "plan.tfplan")
	if err != nil {
		return nil, err
	}
	return ParseShowPlan(out)
}

func ParseShowPlan(raw []byte) ([]Group, error) {
	var sp showPlan
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&sp); err != nil {
		return nil, fmt.Errorf("parse plan: %w", err)
	}
	counts := map[string]map[string]int{}
	order := map[string][]string{}
	diffs := map[string][]string{}
	for _, rc := range sp.ResourceChanges {
		action := actionOf(rc.Change.Actions)
		if rc.Mode == "data" || action == "" {
			continue
		}
		addon := AddonOf(rc.Address)
		if counts[addon] == nil {
			counts[addon] = map[string]int{}
		}
		if counts[addon][action] == 0 {
			order[addon] = append(order[addon], action)
		}
		counts[addon][action]++
		diffs[addon] = append(diffs[addon], resourceDiff(rc.Address, rc.Type, action, rc.Change)...)
	}
	out := make([]Group, 0, len(counts))
	for addon, n := range counts {
		parts := make([]string, 0, len(order[addon]))
		for _, a := range order[addon] {
			parts = append(parts, fmt.Sprintf("%d to %s", n[a], a))
		}
		out = append(out, Group{Addon: addon, Actions: strings.Join(parts, ", "), Diff: capLines(diffs[addon])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Addon < out[j].Addon })
	return out, nil
}

func actionOf(actions []string) string {
	switch strings.Join(actions, ",") {
	case "create", "update", "delete":
		return actions[0]
	case "delete,create", "create,delete":
		return "replace"
	}
	return ""
}

func AddonOf(address string) string {
	_, name, ok := strings.Cut(address, ".")
	if !ok {
		return address
	}
	if i := strings.IndexByte(name, '['); i >= 0 {
		name = name[:i]
	}
	for prefix, addon := range map[string]string{
		"metallb": "metallb", "traefik": "traefik", "gateway_api": "traefik", "ingress_nginx": "traefik", "runtimeclass": "gvisor",
		"metrics_server": "metrics-server", "cert_manager": "cert-manager", "flux": "flux", "longhorn": "longhorn", "builds": "builds", "backup": "backup",
	} {
		if name == prefix || strings.HasPrefix(name, prefix+"_") {
			return addon
		}
	}
	return name
}
