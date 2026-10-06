package cluster

import (
	"strings"
	"testing"

	"github.com/mikael/kubit/internal/config"
)

func TestTalosRejectsAControlPlanePatchOnAWorker(t *testing.T) {
	b := bundle(t)
	decl := "apiVersion: kubit.dev/v1\nkind: Cluster\nmetadata: {name: c}\nspec:\n%s  nodes:\n" +
		"    - {hostname: cp, ip: 10.0.0.1, role: controlplane, installDisk: {path: /dev/sda}}\n" +
		"    - {hostname: w, ip: 10.0.0.2, role: worker, installDisk: {path: /dev/sda}}\n"
	parse := func(patches string) *config.Cluster {
		c, err := config.Parse([]byte(strings.Replace(decl, "%s", patches, 1)))
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	if err := CheckDeclaration(parse(""), b); err != nil {
		t.Fatalf("a plain cluster checks: %v", err)
	}
	err := CheckDeclaration(parse("  patches: [ { apiVersion: v1alpha1, kind: KubeAPIServerConfig, extraArgs: { audit-log-maxage: \"7\" } } ]\n"), b)
	if err == nil {
		t.Fatal("a control-plane document patched onto every node must fail")
	}
	if msg := err.Error(); !strings.Contains(msg, "node w (worker)") || !strings.Contains(msg, "only allowed on control plane") || strings.Contains(msg, "node cp ") {
		t.Errorf("error = %v", err)
	}
	cpOnly, err := config.Parse([]byte(strings.Replace(strings.Replace(decl, "%s", "", 1), "installDisk: {path: /dev/sda}}", "installDisk: {path: /dev/sda}, patches: [ { apiVersion: v1alpha1, kind: KubeAPIServerConfig, extraArgs: { audit-log-maxage: \"7\" } } ]}", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckDeclaration(cpOnly, b); err != nil {
		t.Errorf("the same patch on the control plane node checks: %v", err)
	}
}
