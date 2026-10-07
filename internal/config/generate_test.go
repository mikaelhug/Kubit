package config_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/mikaelhug/kubit/internal/config"
	blockpb "github.com/siderolabs/talos/pkg/machinery/api/resource/definitions/block"
	"github.com/siderolabs/talos/pkg/machinery/cel/celenv"
	talosconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/types/block"
	"github.com/siderolabs/talos/pkg/machinery/config/types/k8s"
	"github.com/siderolabs/talos/pkg/machinery/config/types/runtime"
	"go.yaml.in/yaml/v4"
)

type metalMode struct{}

func (metalMode) String() string        { return "metal" }
func (metalMode) RequiresInstall() bool { return true }
func (metalMode) InContainer() bool     { return false }

const installer = "factory.talos.dev/metal-installer/abc:v1.14.0"

func generateSample(t *testing.T) (*config.Cluster, *config.Generated) {
	t.Helper()
	c, err := config.Parse([]byte(sampleCluster))
	if err != nil {
		t.Fatal(err)
	}
	g, err := config.Generate(c, sharedSecrets(t), installer)
	if err != nil {
		t.Fatal(err)
	}
	return c, g
}

func load(t *testing.T, b []byte) talosconfig.Provider {
	t.Helper()
	cfg, err := configloader.NewFromBytes(b)
	if err != nil {
		t.Fatalf("machinery rejects generated config: %v", err)
	}
	if _, err := cfg.Validate(metalMode{}); err != nil {
		t.Fatalf("metal-mode validation: %v", err)
	}
	return cfg
}

func doc[T any](t *testing.T, cfg talosconfig.Provider) T {
	t.Helper()
	for _, d := range cfg.Documents() {
		if v, ok := d.(T); ok {
			return v
		}
	}
	var zero T
	t.Fatalf("document %T missing", zero)
	return zero
}

func hasDoc[T any](cfg talosconfig.Provider) bool {
	for _, d := range cfg.Documents() {
		if _, ok := d.(T); ok {
			return true
		}
	}
	return false
}

func TestGeneratedConfigPassesTalosValidation(t *testing.T) {
	for _, decl := range []string{sampleCluster, staticCluster} {
		c, err := config.Parse([]byte(decl))
		if err != nil {
			t.Fatal(err)
		}
		g, err := config.Generate(c, sharedSecrets(t), installer)
		if err != nil {
			t.Fatal(err)
		}
		if len(g.Nodes) != len(c.Spec.Nodes) {
			t.Fatalf("%d configs for %d nodes", len(g.Nodes), len(c.Spec.Nodes))
		}
		for _, b := range g.Nodes {
			load(t, b)
		}
	}
}

func TestInstallNeverWipesTheDisk(t *testing.T) {
	_, g := generateSample(t)
	for host, b := range g.Nodes {
		if w := doc[*runtime.UnattendedInstallConfigV1Alpha1](t, load(t, b)).ProvisioningSpec.Wipe; w == nil || *w {
			t.Errorf("%s: install must not wipe the disk", host)
		}
	}
}

func TestDataVolumeMatchesOnlyItsOwnDisk(t *testing.T) {
	_, g := generateSample(t)
	var vols []*block.UserVolumeConfigV1Alpha1
	for _, d := range load(t, g.Nodes["worker-01"]).Documents() {
		if v, ok := d.(*block.UserVolumeConfigV1Alpha1); ok {
			vols = append(vols, v)
		}
	}
	if len(vols) != 2 {
		t.Fatalf("worker-01 has %d user volumes, want 2", len(vols))
	}
	for i, want := range []string{"/dev/vdb", "/dev/vdc"} {
		for _, disk := range []string{want, "/dev/vda"} {
			ok, err := vols[i].ProvisioningSpec.DiskSelectorSpec.Match.EvalBool(celenv.DiskLocator(), map[string]any{"disk": &blockpb.DiskSpec{DevPath: disk}})
			if err != nil || ok != (disk == want) {
				t.Errorf("volume %d on %s: matched=%v err=%v", i, disk, ok, err)
			}
		}
	}
}

func TestOverlappingDataDisksAreRefused(t *testing.T) {
	for _, bad := range []string{"dataDisks: [/dev/vda]", "dataDisks: [/dev/vdb, /dev/vdb]"} {
		y := strings.Replace(sampleCluster, "dataDisks: [/dev/vdb, /dev/vdc]", bad, 1)
		if _, err := config.Parse([]byte(y)); err == nil {
			t.Errorf("%s must be rejected", bad)
		}
	}
}

func TestRestoredSecretsKeepClusterIdentity(t *testing.T) {
	c, g1 := generateSample(t)
	raw, err := yaml.Marshal(g1.Secrets)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := config.ParseSecrets(raw)
	if err != nil {
		t.Fatal(err)
	}
	g2, err := config.Generate(c, restored, installer)
	if err != nil {
		t.Fatalf("generate from restored bundle: %v", err)
	}
	if g2.Secrets.Cluster.ID != g1.Secrets.Cluster.ID || g2.Secrets.Cluster.Secret != g1.Secrets.Cluster.Secret || g2.Talosconfig == nil {
		t.Error("restored bundle must keep identity and produce a talosconfig")
	}
}

const staticCluster = `
apiVersion: kubit.dev/v1
kind: Cluster
metadata: { name: static }
spec:
  network:
    nameservers: [192.168.64.1, 1.1.1.1]
    ntp: [pool.ntp.org]
  nodes:
    - { hostname: cp-01, ip: 192.168.64.2, mac: "52:54:00:4b:49:01", role: controlplane, installDisk: { path: /dev/vda } }
    - { hostname: gpu-01, ip: 192.168.64.3, mac: "52:54:00:4b:49:02", kvm: true, installDisk: { selector: { minSize: 100GB, type: nvme } },
        labels: { workload: gpu, rack: a1 }, taints: { nvidia.com/gpu: "true:NoSchedule" }, annotations: { owner: ml },
        network: { addresses: [192.168.64.150/24], gateway: 192.168.64.1, nameservers: [9.9.9.9], vlan: 0, mtu: 1500 } }
    - { hostname: vlan-01, ip: 192.168.64.4, mac: "52:54:00:4b:49:03", role: worker, installDisk: { path: /dev/vda },
        network: { addresses: [10.20.0.5/24], gateway: 10.20.0.1, vlan: 20 } }
`

func TestNodeConflictsAndInvalidTaintsAreRefused(t *testing.T) {
	cases := map[string]string{
		"bad taint effect": strings.Replace(staticCluster, `"true:NoSchedule"`, `"true:Sometimes"`, 1),
		"duplicate mac":    strings.Replace(staticCluster, "52:54:00:4b:49:02", "52:54:00:4b:49:01", 1),
		"address is vip":   strings.Replace(staticCluster, "spec:\n  network:", "spec:\n  controlPlane: { vip: 192.168.64.150 }\n  network:", 1),
	}
	for name, doc := range cases {
		if _, err := config.Parse([]byte(doc)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestSystemVolumeSplitIsALayoutChange(t *testing.T) {
	c, err := config.Parse([]byte(strings.Replace(sampleCluster, "spec:\n", "spec:\n  storage: { systemDisk: true }\n", 1)))
	if err != nil {
		t.Fatal(err)
	}
	c.Spec.Platform.Longhorn.Enabled = true
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	g, err := config.Generate(c, sharedSecrets(t), installer)
	if err != nil {
		t.Fatal(err)
	}
	var vol *block.UserVolumeConfigV1Alpha1
	for _, d := range load(t, g.Nodes["cp-01"]).Documents() {
		if v, ok := d.(*block.UserVolumeConfigV1Alpha1); ok {
			vol = v
		}
	}
	if vol == nil {
		t.Fatal("cp-01 has no system data volume")
	}
	for _, system := range []bool{true, false} {
		ok, err := vol.ProvisioningSpec.DiskSelectorSpec.Match.EvalBool(celenv.DiskLocator(), map[string]any{"disk": &blockpb.DiskSpec{DevPath: "/dev/vda"}, "system_disk": system})
		if err != nil || ok != system {
			t.Errorf("system_disk=%v: matched=%v err=%v", system, ok, err)
		}
	}
	if vol, err := config.LayoutChange(g.Nodes["worker-01"], g.Nodes["cp-01"]); err != nil || vol == "" {
		t.Errorf("a system volume split is a layout change: %q %v", vol, err)
	}
	if vol, err := config.LayoutChange(g.Nodes["cp-01"], g.Nodes["cp-01"]); err != nil || vol != "" {
		t.Errorf("the same config is no layout change: %q %v", vol, err)
	}
}

func TestSystemDiskUntouchedWithoutLonghorn(t *testing.T) {
	c, err := config.Parse([]byte(strings.Replace(sampleCluster, "spec:\n", "spec:\n  storage: { systemDisk: true }\n", 1)))
	if err != nil {
		t.Fatal(err)
	}
	g, err := config.Generate(c, sharedSecrets(t), installer)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range load(t, g.Nodes["cp-01"]).Documents() {
		if v, ok := d.(*block.UserVolumeConfigV1Alpha1); ok && v.MetaName == "data-system" {
			t.Error("without Longhorn the system disk stays with Talos")
		}
		if v, ok := d.(*block.VolumeConfigV1Alpha1); ok && v.MetaName == "EPHEMERAL" && !v.ProvisioningSpec.ProvisioningMaxSize.IsZero() {
			t.Error("without Longhorn EPHEMERAL must not be capped")
		}
	}
}

func TestGenerateOIDCKeepsAnonymousHealth(t *testing.T) {
	c, err := config.Parse([]byte(sampleCluster))
	if err != nil {
		t.Fatal(err)
	}
	c.Spec.Auth.OIDC = &config.ClusterOIDC{Issuer: "https://sso.example/realms/ops", ClientID: "kubernetes"}
	g, err := config.Generate(c, sharedSecrets(t), installer)
	if err != nil {
		t.Fatal(err)
	}
	authn := doc[*k8s.KubeAuthenticationConfigV1Alpha1](t, load(t, g.Nodes["cp-01"])).AuthConfig.Object
	anon, _ := authn["anonymous"].(map[string]any)
	if anon == nil || anon["enabled"] != true {
		t.Fatalf("anonymous = %v", authn["anonymous"])
	}
	var paths []string
	for _, cond := range anon["conditions"].([]any) {
		paths = append(paths, cond.(map[string]any)["path"].(string))
	}
	for _, want := range []string{"/livez", "/readyz", "/healthz"} {
		if !slices.Contains(paths, want) {
			t.Errorf("anonymous paths %v lack %s", paths, want)
		}
	}
}

func TestEndpointHostIsInTheAPIServerCertificate(t *testing.T) {
	c, err := config.Parse([]byte(strings.Replace(sampleCluster, "    vip: 192.168.64.9\n", "    vip: 192.168.64.9\n    endpoint: https://k8s.example.lan:6443\n", 1)))
	if err != nil {
		t.Fatal(err)
	}
	g, err := config.Generate(c, sharedSecrets(t), installer)
	if err != nil {
		t.Fatal(err)
	}
	sans := doc[*k8s.KubeAPIServerConfigV1Alpha1](t, load(t, g.Nodes["cp-01"])).PodCertExtraSANs
	for _, want := range []string{"k8s.example.lan", "192.168.64.9"} {
		if n := slices.Index(sans, want); n < 0 || slices.Index(sans[n+1:], want) >= 0 {
			t.Errorf("SANs %v must hold %s exactly once", sans, want)
		}
	}
}
