package cluster

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/store"
)

func TestControlPlaneOnlyPatchRejectedOnWorker(t *testing.T) {
	crypto, _ := store.NewCrypto(bytes.Repeat([]byte{5}, 32))
	st, err := store.Open(t.TempDir(), crypto)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := NewManager(st, t.TempDir())
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
	if err := m.CheckCluster(t.Context(), parse("")); err != nil {
		t.Fatalf("a plain cluster checks: %v", err)
	}
	err = m.CheckCluster(t.Context(), parse("  patches: [ { apiVersion: v1alpha1, kind: KubeAPIServerConfig, extraArgs: { audit-log-maxage: \"7\" } } ]\n"))
	if err == nil {
		t.Fatal("a control-plane document patched onto every node must fail")
	}
	if msg := err.Error(); !strings.Contains(msg, "node w (worker)") || !strings.Contains(msg, "only allowed on control plane") || strings.Contains(msg, "node cp ") {
		t.Errorf("error = %v", err)
	}
	cpOnly := "  pools: [ { name: controlplane, role: controlplane, patches: [ { apiVersion: v1alpha1, kind: KubeAPIServerConfig, extraArgs: { audit-log-maxage: \"7\" } } ] }, { name: worker, role: worker } ]\n"
	if err := m.CheckCluster(t.Context(), parse(cpOnly)); err != nil {
		t.Errorf("the same patch on the control-plane pool checks: %v", err)
	}
}
