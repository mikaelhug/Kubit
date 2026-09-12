package config_test

import (
	"strings"
	"testing"

	"github.com/mikael/kubit/internal/config"
	talosconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/types/k8s"
	"github.com/siderolabs/talos/pkg/machinery/config/types/network"
	"github.com/siderolabs/talos/pkg/machinery/config/types/runtime"
	"go.yaml.in/yaml/v4"
)

// metalMode mirrors what a bare-metal Talos node validates against.
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
	g, err := config.Generate(c, nil, installer)
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

func TestGenerateEveryNodeValidates(t *testing.T) {
	c, g := generateSample(t)
	if len(g.Nodes) != len(c.Spec.Nodes) {
		t.Fatalf("%d configs for %d nodes", len(g.Nodes), len(c.Spec.Nodes))
	}
	for host, b := range g.Nodes {
		cfg := load(t, b)
		if got := doc[*network.HostnameConfigV1Alpha1](t, cfg).ConfigHostname; got != host {
			t.Errorf("%s: hostname doc says %q", host, got)
		}
	}
	if g.Talosconfig == nil || g.Secrets == nil {
		t.Error("talosconfig and secrets must be returned")
	}
}

func TestGenerateGVisorRequirements(t *testing.T) {
	_, g := generateSample(t)
	cfg := load(t, g.Nodes["worker-01"])
	// siderolabs/extensions gvisor README: user.max_user_namespaces = 11255.
	if got := doc[*runtime.SysctlConfigV1Alpha1](t, cfg).Params["user.max_user_namespaces"]; got != "11255" {
		t.Errorf("sysctl user.max_user_namespaces = %q", got)
	}
	labels := doc[*k8s.KubeNodeConfigV1Alpha1](t, cfg).LabelsConfig
	if labels["sandbox.runtime/gvisor"] != "true" || labels["sandbox.runtime/gvisor-kvm"] != "true" {
		t.Errorf("worker-01 (kvm) labels = %v", labels)
	}
	cp := load(t, g.Nodes["cp-01"])
	if l := doc[*k8s.KubeNodeConfigV1Alpha1](t, cp).LabelsConfig; l["sandbox.runtime/gvisor"] != "true" || l["sandbox.runtime/gvisor-kvm"] != "" {
		t.Errorf("cp-01 (no kvm) labels = %v", l)
	}
}

func TestGenerateInstallDisk(t *testing.T) {
	_, g := generateSample(t)
	byPath := doc[*runtime.UnattendedInstallConfigV1Alpha1](t, load(t, g.Nodes["cp-01"]))
	if byPath.Installer.Image != installer {
		t.Errorf("installer image = %q", byPath.Installer.Image)
	}
	if got := byPath.ProvisioningSpec.DiskSelector.Match.String(); got != `disk.dev_path == "/dev/vda"` {
		t.Errorf("path selector = %q", got)
	}
	bySel := doc[*runtime.UnattendedInstallConfigV1Alpha1](t, load(t, g.Nodes["cp-02"]))
	got := bySel.ProvisioningSpec.DiskSelector.Match.String()
	for _, want := range []string{"disk.size >= 10u * GB", `disk.transport == "virtio"`, "!disk.cdrom"} {
		if !strings.Contains(got, want) {
			t.Errorf("selector %q lacks %q", got, want)
		}
	}
	if bySel.ProvisioningSpec.Wipe == nil || *bySel.ProvisioningSpec.Wipe {
		t.Error("install must not wipe the disk")
	}
}

func TestGenerateControlPlaneVIP(t *testing.T) {
	_, g := generateSample(t)
	withMAC := load(t, g.Nodes["cp-01"])
	vip := doc[*network.Layer2VIPConfigV1Alpha1](t, withMAC)
	if vip.Name() != "192.168.64.9" || vip.LinkName != "uplink" {
		t.Errorf("VIP doc = %+v", vip)
	}
	if sel := doc[*network.LinkAliasConfigV1Alpha1](t, withMAC).Selector.Match.String(); sel != `mac(link.permanent_addr) == "52:54:00:4b:49:01"` {
		t.Errorf("cp-01 uplink selector = %q", sel)
	}
	if sel := doc[*network.LinkAliasConfigV1Alpha1](t, load(t, g.Nodes["cp-02"])).Selector.Match.String(); sel != `link.type == 1 && link.kind == ""` {
		t.Errorf("cp-02 (no mac) uplink selector = %q", sel)
	}
	worker := load(t, g.Nodes["worker-01"])
	if hasDoc[*network.Layer2VIPConfigV1Alpha1](worker) || hasDoc[*network.LinkAliasConfigV1Alpha1](worker) {
		t.Error("workers must not carry the VIP")
	}
	if !strings.Contains(string(g.Nodes["cp-01"]), "192.168.64.9") {
		t.Error("VIP should appear as API server SAN / endpoint")
	}
}

func TestGenerateSchedulingOnControlPlanes(t *testing.T) {
	c, err := config.Parse([]byte(sampleCluster))
	if err != nil {
		t.Fatal(err)
	}
	g, err := config.Generate(c, nil, installer)
	if err != nil {
		t.Fatal(err)
	}
	cp := doc[*k8s.KubeNodeConfigV1Alpha1](t, load(t, g.Nodes["cp-01"]))
	if len(cp.TaintsConfig) != 0 {
		t.Errorf("schedulable control plane must not be tainted: %v", cp.TaintsConfig)
	}
	if _, excluded := cp.LabelsConfig["node.kubernetes.io/exclude-from-external-load-balancers"]; excluded {
		t.Error("schedulable control plane must be eligible for MetalLB announcements")
	}
	f := false
	c.Spec.ControlPlane.AllowScheduling = &f
	g, err = config.Generate(c, nil, installer)
	if err != nil {
		t.Fatal(err)
	}
	cp = doc[*k8s.KubeNodeConfigV1Alpha1](t, load(t, g.Nodes["cp-01"]))
	if cp.TaintsConfig["node-role.kubernetes.io/control-plane"] != "NoSchedule" {
		t.Errorf("dedicated control plane must be tainted NoSchedule: %v", cp.TaintsConfig)
	}
	if _, excluded := cp.LabelsConfig["node.kubernetes.io/exclude-from-external-load-balancers"]; !excluded {
		t.Error("dedicated control plane keeps Talos' exclude-from-external-load-balancers label")
	}
}

func TestGenerateReusesSecrets(t *testing.T) {
	c, g1 := generateSample(t)
	g2, err := config.Generate(c, g1.Secrets, installer)
	if err != nil {
		t.Fatal(err)
	}
	if g1.Secrets.Cluster.ID != g2.Secrets.Cluster.ID || g1.Secrets.Cluster.Secret != g2.Secrets.Cluster.Secret {
		t.Error("passing a bundle must keep the cluster identity")
	}
	if _, g3 := generateSample(t); g3.Secrets.Cluster.ID == g1.Secrets.Cluster.ID {
		t.Error("nil bundle must mint fresh secrets")
	}
	// The stored form is secrets.yaml; a restored bundle must still be able to sign.
	raw, err := yaml.Marshal(g1.Secrets)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := config.ParseSecrets(raw)
	if err != nil {
		t.Fatal(err)
	}
	g4, err := config.Generate(c, restored, installer)
	if err != nil {
		t.Fatalf("generate from restored bundle: %v", err)
	}
	if g4.Secrets.Cluster.ID != g1.Secrets.Cluster.ID || g4.Talosconfig == nil {
		t.Error("restored bundle must keep identity and produce a talosconfig")
	}
}
