package tofu_test

import (
	"os"
	"testing"

	"github.com/mikael/kubit/internal/tofu"
)

// Fixture: a real `tofu show -json` of a plan that enables cert-manager and widens the
// MetalLB pool on a cluster where everything else is already applied.
func TestParseShowPlan(t *testing.T) {
	raw, err := os.ReadFile("testdata/plan-changes.json")
	if err != nil {
		t.Fatal(err)
	}
	d, err := tofu.ParseShowPlan(raw, []string{"Deprecated Resource: x"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Summary.Add != 1 || d.Summary.Change != 1 || d.Summary.Remove != 0 {
		t.Errorf("summary = %+v, tofu said 1 to add, 1 to change", d.Summary)
	}
	byAddon := map[string]tofu.Group{}
	for _, g := range d.Groups {
		byAddon[g.Addon] = g
	}
	cm, ok := byAddon["cert-manager"]
	if !ok || len(cm.Changes) != 1 || cm.Changes[0].Action != "create" || cm.Changes[0].Type != "helm_release" {
		t.Errorf("cert-manager group: %+v", cm)
	}
	var chart, version string
	for _, a := range cm.Changes[0].Attrs {
		switch a.Key {
		case "chart":
			chart = a.After
		case "version":
			version = a.After
		}
	}
	if chart != "cert-manager" || version == "" {
		t.Errorf("create attrs should show chart and version: chart=%q version=%q", chart, version)
	}
	ml, ok := byAddon["metallb"]
	if !ok || len(ml.Changes) != 1 || ml.Changes[0].Action != "update" || ml.Changes[0].Type != "kubectl_manifest" {
		t.Errorf("metallb group: %+v", ml)
	}
	found := false
	for _, a := range ml.Changes[0].Attrs {
		if a.Key == "yaml_body_parsed" && a.Before != a.After && a.After != "" && !a.Sensitive {
			found = true
		}
		if a.Key == "yaml_body" {
			t.Error("sensitive yaml_body must be replaced by yaml_body_parsed")
		}
	}
	if !found {
		t.Errorf("pool update must show the manifest change: %+v", ml.Changes[0].Attrs)
	}
	for _, g := range d.Groups {
		for _, c := range g.Changes {
			if c.Action == "no-op" || c.Action == "read" {
				t.Errorf("%s: no-op/read changes must be dropped", c.Address)
			}
		}
	}
	if len(d.Warnings) != 1 || d.Timestamp == "" {
		t.Errorf("warnings/timestamp: %+v %q", d.Warnings, d.Timestamp)
	}
}

func TestAddonOf(t *testing.T) {
	for addr, want := range map[string]string{
		"helm_release.metallb[0]":                     "metallb",
		"kubectl_manifest.metallb_pool[0]":            "metallb",
		"kubernetes_namespace_v1.metallb[0]":          "metallb",
		"helm_release.ingress_nginx[0]":               "ingress-nginx",
		"data.kubernetes_service_v1.ingress_nginx[0]": "ingress-nginx",
		"kubectl_manifest.runtimeclass_gvisor_kvm[0]": "gvisor",
		"helm_release.metrics_server[0]":              "metrics-server",
		"helm_release.cert_manager[0]":                "cert-manager",
		"kubectl_manifest.flux_sync[0]":               "flux",
		"helm_release.something_else":                 "something_else",
	} {
		if got := tofu.AddonOf(addr); got != want {
			t.Errorf("AddonOf(%s) = %s, want %s", addr, got, want)
		}
	}
}
