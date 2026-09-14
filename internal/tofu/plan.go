package tofu

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"sort"
	"strings"
)

// PlanDiff is a reviewable rendering of a saved plan: resource changes grouped by the
// add-on they belong to, with the attributes that differ.
type PlanDiff struct {
	Summary  Summary  `json:"summary"`
	Groups   []Group  `json:"groups"`
	Warnings []string `json:"warnings,omitempty"`
	// Timestamp is tofu's plan time (RFC 3339) and doubles as the staleness marker.
	Timestamp string `json:"timestamp"`
}

type Group struct {
	Addon   string   `json:"addon"`
	Changes []Change `json:"changes"`
}

type Change struct {
	Address string `json:"address"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	// Action is one of create, update, replace, delete, read, no-op.
	Action string     `json:"action"`
	Attrs  []AttrDiff `json:"attrs,omitempty"`
}

type AttrDiff struct {
	Key    string `json:"key"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
	// Unknown marks a value only known after apply.
	Unknown   bool `json:"unknown,omitempty"`
	Sensitive bool `json:"sensitive,omitempty"`
}

// showPlan is the subset of `tofu show -json` we read.
type showPlan struct {
	Timestamp       string `json:"timestamp"`
	ResourceChanges []struct {
		Address string `json:"address"`
		Type    string `json:"type"`
		Name    string `json:"name"`
		Mode    string `json:"mode"`
		Change  struct {
			Actions        []string       `json:"actions"`
			Before         map[string]any `json:"before"`
			After          map[string]any `json:"after"`
			AfterUnknown   map[string]any `json:"after_unknown"`
			AfterSensitive any            `json:"after_sensitive"`
		} `json:"change"`
	} `json:"resource_changes"`
}

// ShowPlan renders the saved plan.tfplan in Dir into a PlanDiff.
func (r *Runner) ShowPlan(ctx context.Context, warnings []string) (*PlanDiff, error) {
	cmd := exec.CommandContext(ctx, r.Bin, "show", "-json", "plan.tfplan")
	cmd.Dir = r.Dir
	cmd.Env = r.env()
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("tofu show: %w", err)
	}
	return ParseShowPlan(out, warnings)
}

// ParseShowPlan turns `tofu show -json` output into a PlanDiff.
func ParseShowPlan(raw []byte, warnings []string) (*PlanDiff, error) {
	var sp showPlan
	if err := json.Unmarshal(raw, &sp); err != nil {
		return nil, fmt.Errorf("parse plan: %w", err)
	}
	groups := map[string]*Group{}
	diff := PlanDiff{Groups: []Group{}}
	diff.Timestamp = sp.Timestamp
	diff.Warnings = warnings
	for _, rc := range sp.ResourceChanges {
		action := actionOf(rc.Change.Actions)
		if rc.Mode == "data" || action == "no-op" {
			continue
		}
		switch action {
		case "create":
			diff.Summary.Add++
		case "update":
			diff.Summary.Change++
		case "replace":
			diff.Summary.Add++
			diff.Summary.Remove++
		case "delete":
			diff.Summary.Remove++
		}
		addon := AddonOf(rc.Address)
		g, ok := groups[addon]
		if !ok {
			g = &Group{Addon: addon}
			groups[addon] = g
		}
		sensitive, _ := rc.Change.AfterSensitive.(map[string]any)
		g.Changes = append(g.Changes, Change{
			Address: rc.Address, Type: rc.Type, Name: rc.Name, Action: action,
			Attrs: attrDiffs(rc.Change.Before, rc.Change.After, rc.Change.AfterUnknown, sensitive),
		})
	}
	for _, g := range groups {
		sort.Slice(g.Changes, func(i, j int) bool { return g.Changes[i].Address < g.Changes[j].Address })
		diff.Groups = append(diff.Groups, *g)
	}
	sort.Slice(diff.Groups, func(i, j int) bool { return diff.Groups[i].Addon < diff.Groups[j].Addon })
	return &diff, nil
}

func actionOf(actions []string) string {
	switch strings.Join(actions, ",") {
	case "create":
		return "create"
	case "update":
		return "update"
	case "delete":
		return "delete"
	case "delete,create", "create,delete":
		return "replace"
	case "read":
		return "read"
	default:
		return "no-op"
	}
}

// AddonOf maps a resource address to the add-on it implements, by the naming
// convention of the platform templates (helm_release.metallb, kubectl_manifest.metallb_pool,
// kubectl_manifest.runtimeclass_gvisor, ...).
func AddonOf(address string) string {
	addr := strings.TrimPrefix(address, "data.")
	_, name, ok := strings.Cut(addr, ".")
	if !ok {
		return address
	}
	if i := strings.IndexByte(name, '['); i >= 0 {
		name = name[:i]
	}
	for prefix, addon := range map[string]string{
		"metallb": "metallb", "ingress_nginx": "ingress-nginx", "runtimeclass": "gvisor",
		"metrics_server": "metrics-server", "cert_manager": "cert-manager", "argocd": "argocd",
	} {
		if name == prefix || strings.HasPrefix(name, prefix+"_") {
			return addon
		}
	}
	return name
}

// Attributes that only carry provider noise; they never help a reviewer.
var hiddenAttrs = map[string]bool{"id": true, "metadata": true, "status": true, "timeouts": true, "yaml_incluster": true, "live_manifest_incluster": true, "wait_for": true}

func attrDiffs(before, after, unknown map[string]any, sensitive map[string]any) []AttrDiff {
	keys := map[string]bool{}
	for k := range before {
		keys[k] = true
	}
	for k := range after {
		keys[k] = true
	}
	for k := range unknown {
		keys[k] = true
	}
	// The kubectl provider marks yaml_body sensitive but exposes the same manifest as
	// yaml_body_parsed; show the readable one only.
	if keys["yaml_body_parsed"] {
		delete(keys, "yaml_body")
	}
	var out []AttrDiff
	for k := range keys {
		if hiddenAttrs[k] {
			continue
		}
		b, a := before[k], after[k]
		_, isUnknown := unknown[k]
		if isUnknown {
			if v, ok := unknown[k].(bool); ok && !v {
				isUnknown = false
			}
		}
		if !isUnknown && reflect.DeepEqual(b, a) {
			continue
		}
		if isNil(b) && isNil(a) && !isUnknown {
			continue
		}
		d := AttrDiff{Key: k, Unknown: isUnknown}
		if v, ok := sensitive[k].(bool); ok && v {
			d.Sensitive = true
		} else {
			d.Before, d.After = render(b), render(a)
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func isNil(v any) bool {
	if v == nil {
		return true
	}
	switch t := v.(type) {
	case string:
		return t == ""
	case []any:
		return len(t) == 0
	case map[string]any:
		return len(t) == 0
	case bool:
		return !t
	}
	return false
}

func render(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool, float64:
		return fmt.Sprint(t)
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}
