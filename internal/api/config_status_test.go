package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/store"
	"go.yaml.in/yaml/v4"
)

func TestConfigStatusRoute(t *testing.T) {
	s, st, _ := localServer(t)
	ctx := t.Context()
	c, err := config.Parse([]byte("apiVersion: kubit.dev/v1\nkind: Cluster\nmetadata: {name: c}\nspec:\n  nodes:\n    - {hostname: a, ip: 10.0.0.1, role: controlplane, installDisk: {path: /dev/sda}}\n    - {hostname: b, ip: 10.0.0.2, role: worker, installDisk: {path: /dev/sda}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	gen, err := config.Generate(c, nil, config.FixedInstaller("installer"))
	if err != nil {
		t.Fatal(err)
	}
	bundle, _ := yaml.Marshal(gen.Secrets)
	spec, _ := c.Marshal()
	if err := st.PutCluster(ctx, store.ClusterRow{Name: "c", Spec: spec, State: cluster.StateProvisioning}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutClusterSecrets(ctx, "c", store.ClusterSecrets{SecretsBundle: bundle, Talosconfig: []byte("t")}); err != nil {
		t.Fatal(err)
	}
	for _, n := range c.Spec.Nodes {
		if err := st.UpsertNode(ctx, store.NodeRow{IP: n.IP, Cluster: "c", Hostname: n.Hostname, Role: string(n.Role), Source: "manual", State: cluster.NodeReady}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.PutNodeMachineConfig(ctx, "10.0.0.2", []byte("an older config"), false); err != nil {
		t.Fatal(err)
	}
	get := func() (int, string) {
		rec := call(t, s, http.MethodGet, "/api/v1/clusters/c/config", "")
		return rec.Code, strings.TrimSpace(rec.Body.String())
	}
	if code, body := get(); code != http.StatusOK || body != `{"behind":[]}` {
		t.Errorf("a cluster being installed: %d %s", code, body)
	}
	if err := st.SetClusterState(ctx, "c", cluster.StateReady); err != nil {
		t.Fatal(err)
	}
	if code, body := get(); code != http.StatusOK || body != `{"behind":["b"]}` {
		t.Errorf("a node with an older config: %d %s", code, body)
	}
	if rec := call(t, s, http.MethodGet, "/api/v1/clusters/missing/config", ""); rec.Code != http.StatusNotFound {
		t.Errorf("an unknown cluster: %d %s", rec.Code, rec.Body)
	}
}
