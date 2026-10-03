package config_test

import (
	"strings"
	"testing"

	"github.com/mikael/kubit/internal/config"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	"github.com/siderolabs/talos/pkg/machinery/gendata"
)

func TestParseDefaults(t *testing.T) {
	c, err := config.Parse([]byte(sampleCluster))
	if err != nil {
		t.Fatal(err)
	}
	if c.Spec.TalosVersion != gendata.VersionTag {
		t.Errorf("talosVersion = %q, want machinery's %q", c.Spec.TalosVersion, gendata.VersionTag)
	}
	if c.Spec.KubernetesVersion != "v"+constants.DefaultKubernetesVersion {
		t.Errorf("kubernetesVersion = %q", c.Spec.KubernetesVersion)
	}
	if c.Spec.ControlPlane.Endpoint != "https://192.168.64.9:6443" {
		t.Errorf("endpoint should default to the VIP, got %q", c.Spec.ControlPlane.Endpoint)
	}
	if c.Spec.ControlPlane.AllowScheduling == nil || !*c.Spec.ControlPlane.AllowScheduling {
		t.Error("4 nodes: control planes should be schedulable by default")
	}
	if c.Spec.Network.PodCIDR != "10.244.0.0/16" || c.Spec.Network.ServiceCIDR != "10.96.0.0/12" {
		t.Errorf("CIDR defaults: %+v", c.Spec.Network)
	}
	if len(c.Spec.Extensions) != 1 || c.Spec.Extensions[0] != "siderolabs/gvisor" {
		t.Errorf("gvisor enabled should imply the extension, got %v", c.Spec.Extensions)
	}
	if len(c.ControlPlanes()) != 3 || len(c.Workers()) != 1 {
		t.Errorf("roles: %d cp, %d workers", len(c.ControlPlanes()), len(c.Workers()))
	}
	if nw := c.Spec.Network; nw.Policies == nil || !*nw.Policies || nw.Discovery == nil || !*nw.Discovery {
		t.Errorf("network policies and discovery default on: %+v", nw)
	}
	if c.Spec.Network.Firewall != nil {
		t.Errorf("firewall has no default, got %v", *c.Spec.Network.Firewall)
	}
	if c.Spec.Storage.Encryption != "" {
		t.Errorf("encryption has no default, got %q", c.Spec.Storage.Encryption)
	}
	if !c.Spec.Platform.Traefik.Enabled {
		t.Error("traefik should be enabled")
	}
}

func TestPrereleaseTalosVersionAccepted(t *testing.T) {
	c, err := config.Parse([]byte(strings.Replace(sampleCluster, "spec:\n", "spec:\n  talosVersion: v1.14.0-rc.2\n", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if c.Spec.TalosVersion != "v1.14.0-rc.2" {
		t.Error(c.Spec.TalosVersion)
	}
}

func TestEndpointDefaultsToFirstControlPlaneWithoutVIP(t *testing.T) {
	c, err := config.Parse([]byte(strings.Replace(sampleCluster, "vip: 192.168.64.9", "", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if c.Spec.ControlPlane.Endpoint != "https://192.168.64.2:6443" {
		t.Errorf("endpoint = %q", c.Spec.ControlPlane.Endpoint)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]string{
		"unknown field":      strings.Replace(sampleCluster, "kind: Cluster", "kind: Cluster\nbogus: 1", 1),
		"duplicate ip":       strings.Replace(sampleCluster, "192.168.64.5", "192.168.64.2", 1),
		"duplicate host":     strings.Replace(sampleCluster, "worker-01", "cp-01", 1),
		"bad role":           strings.Replace(sampleCluster, "role: worker", "role: minion", 1),
		"no control plane":   strings.ReplaceAll(sampleCluster, "role: controlplane", "role: worker"),
		"bad mac":            strings.Replace(sampleCluster, "52:54:00:4b:49:01", "nope", 1),
		"disk path+selector": strings.Replace(sampleCluster, "installDisk: { path: /dev/vda }", "installDisk: { path: /dev/vda, selector: { type: nvme } }", 1),
		"no disk":            strings.Replace(sampleCluster, "installDisk: { path: /dev/vda }", "installDisk: {}", 1),
		"bad metallb range":  strings.Replace(sampleCluster, "192.168.64.200-192.168.64.220", "192.168.64.220-192.168.64.200", 1),
		"uppercase hostname": strings.Replace(sampleCluster, "hostname: cp-01", "hostname: CP-01", 1),
		"talos too old":      strings.Replace(sampleCluster, "spec:\n", "spec:\n  talosVersion: v1.13.10\n", 1),
		"static without dns": strings.Replace(sampleCluster, `mac: "52:54:00:4b:49:01", installDisk: { path: /dev/vda }`, `mac: "52:54:00:4b:49:01", installDisk: { path: /dev/vda }, network: { addresses: ["192.168.64.2/24"] }`, 1),
		"flux over http":     sampleCluster + "    flux: { enabled: true, repository: { url: http://example.com/apps.git } }\n",
		"flux path escapes":  sampleCluster + "    flux: { enabled: true, repository: { url: https://example.com/apps.git, path: ../other } }\n",
		"flux interval":      sampleCluster + "    flux: { enabled: true, repository: { url: https://example.com/apps.git, interval: soon } }\n",
		"bad encryption":     strings.Replace(sampleCluster, "spec:\n", "spec:\n  storage: { encryption: luks }\n", 1),
	}
	for name, doc := range cases {
		if _, err := config.Parse([]byte(doc)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

const declaredExtras = `  network: { policies: false, discovery: false, firewall: true }
  storage: { encryption: nodeID }
  patches:
    - machine: { sysctls: { vm.max_map_count: "262144" } }
  pools:
    - name: controlplane
      role: controlplane
      patches: [ { machine: { kernel: { modules: [ { name: br_netfilter } ] } } } ]
`

func TestRoundTrip(t *testing.T) {
	c, err := config.Parse([]byte(strings.Replace(strings.Replace(sampleCluster, "spec:\n", "spec:\n"+declaredExtras, 1),
		"kvm: true,", "kvm: true, tpm: true, watchdog: true, patches: [ { machine: { install: { wipe: false } } } ],", 1)))
	if err != nil {
		t.Fatal(err)
	}
	nw, w := c.Spec.Network, c.Spec.Nodes[3]
	if *nw.Policies || *nw.Discovery || !*nw.Firewall || c.Spec.Storage.Encryption != config.EncryptionNodeID {
		t.Errorf("network %+v storage %+v", nw, c.Spec.Storage)
	}
	if len(c.Spec.Patches) != 1 || len(c.Spec.Pools[0].Patches) != 1 || len(w.Patches) != 1 || !w.TPM || !w.Watchdog {
		t.Errorf("patches %v / %v / %v, node %+v", c.Spec.Patches, c.Spec.Pools[0].Patches, w.Patches, w)
	}
	b, err := c.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	c2, err := config.Parse(b)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if b2, _ := c2.Marshal(); string(b2) != string(b) {
		t.Errorf("marshal not stable:\n%s\n---\n%s", b, b2)
	}
}

func TestFluxRepositoryDefaults(t *testing.T) {
	c, err := config.Parse([]byte(sampleCluster + "    flux: { enabled: true, repository: { url: https://github.com/mikaelhug/kubit-apps.git } }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if r := c.Spec.Platform.Flux.Repository; r.Branch != "main" || r.Path != "./" || r.Interval != "5m" {
		t.Errorf("repository = %+v", r)
	}
}

func TestParseIPRange(t *testing.T) {
	a, b, err := config.ParseIPRange("10.0.0.5 - 10.0.0.9")
	if err != nil || a.String() != "10.0.0.5" || b.String() != "10.0.0.9" {
		t.Errorf("got %v %v %v", a, b, err)
	}
	if _, _, err := config.ParseIPRange("10.0.0.5"); err == nil {
		t.Error("single address should be rejected")
	}
}

func TestRecommend(t *testing.T) {
	cases := []struct {
		n     int
		cp, w int
		sched bool
	}{
		{1, 1, 0, true}, {2, 1, 1, true}, {3, 3, 0, true}, {5, 3, 2, true}, {6, 3, 3, false}, {10, 3, 7, false},
	}
	for _, tc := range cases {
		got := config.Recommend(tc.n)
		if got.ControlPlanes != tc.cp || got.Workers != tc.w || got.AllowScheduling != tc.sched {
			t.Errorf("Recommend(%d) = %+v, want cp=%d w=%d sched=%v", tc.n, got, tc.cp, tc.w, tc.sched)
		}
		if got.HA != (tc.cp == 3) {
			t.Errorf("Recommend(%d).HA = %v", tc.n, got.HA)
		}
	}
}

func TestLegacyArgoCDStillParses(t *testing.T) {
	c, err := config.Parse([]byte(sampleCluster + "    argocd: { enabled: true, values: { server: { replicas: 1 } } }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Spec.Platform.Flux.Enabled || c.Spec.Platform.LegacyArgoCD != nil {
		t.Errorf("platform = %+v", c.Spec.Platform)
	}
	if b, _ := c.Marshal(); strings.Contains(string(b), "argocd") {
		t.Errorf("argocd survived the save:\n%s", b)
	}
}

func TestLegacyIngressNginxFoldsIntoTraefik(t *testing.T) {
	c, err := config.Parse([]byte(strings.Replace(sampleCluster, "    traefik: { enabled: true }\n", "    ingressNginx: { enabled: true, values: { controller: { replicaCount: 2 } } }\n", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if p := c.Spec.Platform; !p.Traefik.Enabled || p.Traefik.Values != nil || p.LegacyIngressNginx != nil {
		t.Errorf("platform = %+v", p)
	}
	b, err := c.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "ingressNginx") || strings.Contains(string(b), "replicaCount") {
		t.Errorf("ingressNginx survived the save:\n%s", b)
	}
	if !strings.Contains(string(b), "traefik:") {
		t.Errorf("traefik missing from the save:\n%s", b)
	}
}

func TestRegistryIP(t *testing.T) {
	for cidr, want := range map[string]string{"10.96.0.0/12": "10.96.0.50", "172.20.8.0/22": "172.20.8.50", "172.20.8.0/24": "", "fd00::/108": "", "nope": ""} {
		c, _ := config.Parse([]byte(sampleCluster))
		c.Spec.Network.ServiceCIDR = cidr
		if got := c.RegistryIP(); got != want {
			t.Errorf("%s: RegistryIP = %q, want %q", cidr, got, want)
		}
	}
}

func TestCheckChange(t *testing.T) {
	load := func(extra string) *config.Cluster {
		c, err := config.Parse([]byte(strings.Replace(sampleCluster, "spec:\n", "spec:\n"+extra, 1)))
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	all := func(c *config.Cluster, v bool) map[string]bool {
		m := map[string]bool{}
		for _, n := range c.Spec.Nodes {
			m[n.IP] = v
		}
		return m
	}
	withoutLonghorn := load("  storage: { systemDisk: true }\n")
	withLonghorn := load("  storage: { systemDisk: true }\n")
	withLonghorn.Spec.Platform.Longhorn.Enabled = true
	if err := config.CheckChange(withoutLonghorn, withLonghorn, all(withLonghorn, true), all(withLonghorn, false)); err == nil || !strings.Contains(err.Error(), "cp-01") {
		t.Errorf("Longhorn onto the system disk of nodes installed without the volume must be refused: %v", err)
	}
	if err := config.CheckChange(withoutLonghorn, withLonghorn, all(withLonghorn, true), all(withLonghorn, true)); err != nil {
		t.Errorf("nodes that kept their volume may get Longhorn back: %v", err)
	}
	if err := config.CheckChange(withoutLonghorn, withLonghorn, nil, nil); err != nil {
		t.Errorf("before install Longhorn may be turned on: %v", err)
	}
	noSplit := load("")
	noSplit.Spec.Platform.Longhorn.Enabled = false
	if err := config.CheckChange(withoutLonghorn, noSplit, all(noSplit, true), all(noSplit, false)); err != nil {
		t.Errorf("storage may change while no node holds a volume: %v", err)
	}
	bigger := load("  storage: { systemDisk: true, ephemeralSize: 80GiB }\n")
	bigger.Spec.Platform.Longhorn.Enabled = true
	if err := config.CheckChange(withLonghorn, bigger, all(bigger, true), all(bigger, true)); err == nil {
		t.Error("storage must stay once nodes hold a volume")
	}
}

func TestValidateTPMNeedsTPMOnEveryNode(t *testing.T) {
	tpm := strings.Replace(sampleCluster, "spec:\n", "spec:\n  storage: { encryption: tpm }\n", 1)
	_, err := config.Parse([]byte(strings.Replace(tpm, "kvm: true,", "kvm: true, tpm: true,", 1)))
	if err == nil || !strings.Contains(err.Error(), "cp-01: no TPM; storage.encryption is tpm") || strings.Contains(err.Error(), "worker-01") {
		t.Errorf("nodes without a TPM must be named: %v", err)
	}
	if _, err := config.Parse([]byte(strings.ReplaceAll(tpm, "arch: arm64,", "arch: arm64, tpm: true,"))); err != nil {
		t.Errorf("TPM on every node: %v", err)
	}
	if _, err := config.Parse([]byte(strings.Replace(sampleCluster, "spec:\n", "spec:\n  storage: { encryption: nodeID }\n", 1))); err != nil {
		t.Errorf("nodeID needs no TPM: %v", err)
	}
	c, err := config.Parse([]byte(strings.ReplaceAll(tpm, "arch: arm64,", "arch: arm64, tpm: true,")))
	if err != nil {
		t.Fatal(err)
	}
	c.Spec.Nodes = append(c.Spec.Nodes, config.Node{Hostname: "worker-02", IP: "192.168.64.6", Pool: "worker", Role: config.RoleWorker, Arch: config.ArchARM64, InstallDisk: config.InstallDisk{Path: "/dev/vda"}})
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "worker-02: no TPM") {
		t.Errorf("a machine without a TPM must not join a TPM-encrypted cluster: %v", err)
	}
}

func TestEncryptionFixedOnceInstalled(t *testing.T) {
	parse := func(storage string) *config.Cluster {
		c, err := config.Parse([]byte(strings.Replace(sampleCluster, "spec:\n", "spec:\n  storage: "+storage+"\n", 1)))
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	off, nodeID := parse("{}"), parse("{ encryption: nodeID }")
	cp01 := map[string]bool{"192.168.64.2": true}
	if err := config.CheckChange(off, nodeID, cp01, nil); err == nil || !strings.Contains(err.Error(), "storage.encryption must stay: cp-01 is installed") {
		t.Errorf("turning encryption on over an installed node must be refused: %v", err)
	}
	if err := config.CheckChange(nodeID, off, cp01, map[string]bool{"192.168.64.2": false}); err == nil {
		t.Error("turning encryption off over an installed node must be refused")
	}
	if err := config.CheckChange(off, nodeID, nil, nil); err != nil {
		t.Errorf("before install encryption may change: %v", err)
	}
	if err := config.CheckChange(nodeID, parse("{ encryption: nodeID, ephemeralSize: 80GiB }"), cp01, nil); err != nil {
		t.Errorf("other storage fields are not encryption's concern: %v", err)
	}
}

func TestEndpointNodeAndNodeIndex(t *testing.T) {
	c, err := config.Parse([]byte(strings.Replace(sampleCluster, "    vip: 192.168.64.9\n", "", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if n, ok := c.EndpointNode(); !ok || n.Hostname != "cp-01" {
		t.Errorf("without a VIP the first control plane hosts the endpoint: %v %v", n.Hostname, ok)
	}
	if i := c.NodeIndex("cp-02"); i != 1 || c.NodeIndex("nope") != -1 {
		t.Errorf("NodeIndex: %d", i)
	}
	vip, err := config.Parse([]byte(sampleCluster))
	if err != nil {
		t.Fatal(err)
	}
	if n, ok := vip.EndpointNode(); ok {
		t.Errorf("a VIP endpoint has no host node, got %s", n.Hostname)
	}
}

func TestControlPlanePoolFoundByRole(t *testing.T) {
	c, err := config.Parse([]byte(`
apiVersion: kubit.dev/v1
kind: Cluster
metadata: { name: cp }
spec:
  pools:
    - { name: cp, role: controlplane, labels: { tier: control } }
  nodes:
    - { hostname: cp-01, ip: 10.0.0.1, role: controlplane, installDisk: { path: /dev/sda } }
    - { hostname: w-01, ip: 10.0.0.2, installDisk: { path: /dev/sda } }
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Spec.Pools) != 2 || c.Spec.Nodes[0].Pool != "cp" || c.Spec.Nodes[1].Pool != "worker" {
		t.Errorf("pools %+v, nodes in %s and %s", c.Spec.Pools, c.Spec.Nodes[0].Pool, c.Spec.Nodes[1].Pool)
	}
	if p := c.ControlPlanePool(); p == nil || p.Name != "cp" {
		t.Errorf("control plane pool: %+v", p)
	}
}
