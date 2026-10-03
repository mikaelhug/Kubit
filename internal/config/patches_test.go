package config_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mikael/kubit/internal/config"
	"github.com/siderolabs/talos/pkg/machinery/config/types/network"
	"github.com/siderolabs/talos/pkg/machinery/config/types/runtime"
)

const patchedCluster = `
  network: { firewall: true }
  patches:
    - machine: { sysctls: { vm.max_map_count: "1", fs.inotify.max_user_watches: "1048576" } }
  pools:
    - name: controlplane
      role: controlplane
    - name: worker
      role: worker
      patches:
        - machine: { sysctls: { vm.max_map_count: "2", net.core.somaxconn: "4096" } }
        - { apiVersion: v1alpha1, kind: NetworkRuleConfig, name: kubelet, $patch: delete }
`

func TestPatchOrderAndDelete(t *testing.T) {
	decl := strings.Replace(sampleCluster, "spec:\n", "spec:"+patchedCluster, 1)
	decl = strings.Replace(decl, "kvm: true,", `kvm: true, patches: [ { machine: { sysctls: { vm.max_map_count: "3" } } } ],`, 1)
	c, err := config.Parse([]byte(decl))
	if err != nil {
		t.Fatal(err)
	}
	g, err := config.Generate(c, sharedSecrets(t), config.FixedInstaller(installer))
	if err != nil {
		t.Fatal(err)
	}
	sysctls := func(host string) map[string]string {
		return load(t, g.Nodes[host]).Machine().Sysctls()
	}
	for host, want := range map[string][3]string{
		"cp-01":     {"1", "1048576", ""},
		"worker-01": {"3", "1048576", "4096"},
	} {
		s := sysctls(host)
		if got := [3]string{s["vm.max_map_count"], s["fs.inotify.max_user_watches"], s["net.core.somaxconn"]}; got != want {
			t.Errorf("%s sysctls = %v, want %v", host, got, want)
		}
	}
	if doc[*runtime.SysctlConfigV1Alpha1](t, load(t, g.Nodes["worker-01"])).Params["user.max_user_namespaces"] != "11255" {
		t.Error("patches must merge into the generated sysctls, not replace them")
	}
	if rules(load(t, g.Nodes["worker-01"]))["kubelet"] != nil {
		t.Error("$patch: delete in the worker pool keeps the kubelet rule")
	}
	if rules(load(t, g.Nodes["cp-01"]))["kubelet"] == nil {
		t.Error("the worker pool's delete reached a control plane")
	}
}

func TestPatchesAbsentKeepBytes(t *testing.T) {
	_, plain := generateSample(t)
	c, err := config.Parse([]byte(strings.Replace(sampleCluster, "spec:\n", "spec:\n  patches: []\n", 1)))
	if err != nil {
		t.Fatal(err)
	}
	g, err := config.Generate(c, sharedSecrets(t), config.FixedInstaller(installer))
	if err != nil {
		t.Fatal(err)
	}
	for host, b := range plain.Nodes {
		if !bytes.Equal(b, g.Nodes[host]) {
			t.Errorf("%s: an empty patch list changed the output", host)
		}
	}
	if hasDoc[*network.DefaultActionConfigV1Alpha1](load(t, g.Nodes["cp-01"])) {
		t.Error("no firewall without the key")
	}
}

func TestValidateRejectsUnloadablePatch(t *testing.T) {
	for _, tc := range []struct{ decl, want string }{
		{strings.Replace(sampleCluster, "spec:\n", "spec:\n  patches: [ { machine: { nosuchfield: 1 } } ]\n", 1), "patches[0]: "},
		{strings.Replace(sampleCluster, "spec:\n", "spec:\n  pools: [ { name: controlplane, role: controlplane }, { name: worker, role: worker, patches: [ {}, { apiVersion: v1alpha1, kind: NoSuchKind } ] } ]\n", 1), "pools[1].patches[1]: "},
		{strings.Replace(sampleCluster, "kvm: true,", "kvm: true, patches: [ { cluster: { bogus: true } } ],", 1), "worker-01.patches[0]: "},
	} {
		_, err := config.Parse([]byte(tc.decl))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("want an error with %q, got %v", tc.want, err)
		}
	}
}
