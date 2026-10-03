package cluster

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/store"
	"go.yaml.in/yaml/v4"
)

const driftCluster = `
apiVersion: kubit.dev/v1
kind: Cluster
metadata: { name: lab }
spec:
  nodes:
    - { hostname: cp-01, ip: 10.0.0.1, role: controlplane, installDisk: { path: /dev/sda } }
    - { hostname: w-01, ip: 10.0.0.2, role: worker, installDisk: { path: /dev/sda } }
    - { hostname: w-02, ip: 10.0.0.3, role: worker, installDisk: { path: /dev/sda } }
`

func driftManager(t *testing.T) (*Manager, *store.Store, *config.Generated) {
	t.Helper()
	st := testStore(t)
	ctx := t.Context()
	m := NewManager(st, t.TempDir())
	c, err := config.Parse([]byte(driftCluster))
	if err != nil {
		t.Fatal(err)
	}
	gen, err := config.Generate(c, nil, m.installer(c))
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := yaml.Marshal(gen.Secrets)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SaveCluster(ctx, c, StateReady); err != nil {
		t.Fatal(err)
	}
	if err := st.PutClusterSecrets(ctx, "lab", store.ClusterSecrets{SecretsBundle: bundle, Talosconfig: []byte("talosconfig")}); err != nil {
		t.Fatal(err)
	}
	for _, n := range c.Spec.Nodes {
		if err := m.recordNode(ctx, c, n, NodeReady, nil); err != nil {
			t.Fatal(err)
		}
	}
	return m, st, gen
}

func storeApplied(t *testing.T, m *Manager, hostnames ...string) {
	t.Helper()
	ctx := t.Context()
	c, _, err := m.LoadCluster(ctx, "lab")
	if err != nil {
		t.Fatal(err)
	}
	_, gen, err := m.generateNodeConfigs(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range c.Spec.Nodes {
		if slices.Contains(hostnames, n.Hostname) {
			if err := m.Store.PutNodeMachineConfig(ctx, n.IP, gen.Nodes[n.Hostname], false); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func editDeclaration(t *testing.T, m *Manager, edit func(c *config.Cluster)) {
	t.Helper()
	c, _, err := m.LoadCluster(t.Context(), "lab")
	if err != nil {
		t.Fatal(err)
	}
	edit(c)
	if err := m.SaveCluster(t.Context(), c, ""); err != nil {
		t.Fatal(err)
	}
}

func behind(t *testing.T, m *Manager) []string {
	t.Helper()
	st, err := m.ConfigStatus(t.Context(), "lab")
	if err != nil {
		t.Fatal(err)
	}
	return st.Behind
}

func TestConfigStatusBehindAfterEdit(t *testing.T) {
	m, _, _ := driftManager(t)
	storeApplied(t, m, "cp-01", "w-01", "w-02")
	editDeclaration(t, m, func(c *config.Cluster) {
		c.Spec.Nodes[1].Patches = []map[string]any{{"machine": map[string]any{"sysctls": map[string]any{"vm.max_map_count": "262144"}}}}
	})
	if got := behind(t, m); !slices.Equal(got, []string{"w-01"}) {
		t.Errorf("a node patch puts only that node behind: %v", got)
	}
	editDeclaration(t, m, func(c *config.Cluster) { c.Spec.Network.NTP = []string{"time.example"} })
	if got := behind(t, m); !slices.Equal(got, []string{"cp-01", "w-01", "w-02"}) {
		t.Errorf("a cluster-wide change puts every node behind, in declaration order: %v", got)
	}
}

func TestConfigStatusClearAfterStore(t *testing.T) {
	m, st, created := driftManager(t)
	c, _, err := m.LoadCluster(t.Context(), "lab")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range c.Spec.Nodes {
		if err := st.PutNodeMachineConfig(t.Context(), n.IP, created.Nodes[n.Hostname], false); err != nil {
			t.Fatal(err)
		}
	}
	if got := behind(t, m); len(got) != 0 {
		t.Errorf("the configs stored at create are current: %v", got)
	}
	editDeclaration(t, m, func(c *config.Cluster) { c.Spec.Network.NTP = []string{"time.example"} })
	storeApplied(t, m, "cp-01", "w-01", "w-02")
	status, err := m.ConfigStatus(t.Context(), "lab")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(status); string(b) != `{"behind":[]}` {
		t.Errorf("applying the edit clears it: %s", b)
	}
}

func TestConfigStatusSkipsNodesWithoutStoredConfig(t *testing.T) {
	m, st, _ := driftManager(t)
	storeApplied(t, m, "cp-01", "w-02")
	editDeclaration(t, m, func(c *config.Cluster) { c.Spec.Network.NTP = []string{"time.example"} })
	if got := behind(t, m); !slices.Equal(got, []string{"cp-01", "w-02"}) {
		t.Errorf("a node without a stored config is not behind: %v", got)
	}
	if err := st.SetClusterState(t.Context(), "lab", StateProvisioning); err != nil {
		t.Fatal(err)
	}
	if got := behind(t, m); got == nil || len(got) != 0 {
		t.Errorf("a cluster being installed reports nothing: %v", got)
	}
}
