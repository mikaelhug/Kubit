package config_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mikael/kubit/internal/config"
	talosconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/types/block"
	"github.com/siderolabs/talos/pkg/machinery/config/types/k8s"
	"github.com/siderolabs/talos/pkg/machinery/config/types/network"
	"github.com/siderolabs/talos/pkg/machinery/config/types/runtime"
	blockres "github.com/siderolabs/talos/pkg/machinery/resources/block"
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
	g, err := config.Generate(c, nil, config.FixedInstaller(installer))
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

// README: data disks become whole-disk xfs user volumes mounted at /var/mnt/data-N,
// in declaration order, never touching the install disk; the node is labelled with
// the count.
func TestGenerateDataDisks(t *testing.T) {
	_, g := generateSample(t)
	cfg := load(t, g.Nodes["worker-01"])
	var vols []*block.UserVolumeConfigV1Alpha1
	for _, d := range cfg.Documents() {
		if v, ok := d.(*block.UserVolumeConfigV1Alpha1); ok {
			vols = append(vols, v)
		}
	}
	if len(vols) != 2 {
		t.Fatalf("worker-01 has %d user volumes, want 2", len(vols))
	}
	for i, want := range []string{"/dev/vdb", "/dev/vdc"} {
		v := vols[i]
		if v.MetaName != fmt.Sprintf("data-%d", i+1) || *v.VolumeType != blockres.VolumeTypeDisk || v.FilesystemSpec.FilesystemType != blockres.FilesystemTypeXFS {
			t.Errorf("volume %d = %s %v %v", i, v.MetaName, v.VolumeType, v.FilesystemSpec.FilesystemType)
		}
		if got := v.ProvisioningSpec.DiskSelectorSpec.Match.String(); got != fmt.Sprintf(`disk.dev_path == %q && !system_disk`, want) {
			t.Errorf("volume %d selector = %q", i, got)
		}
	}
	if config.DataMount(2) != "/var/mnt/data-2" {
		t.Errorf("mount = %s", config.DataMount(2))
	}
	if l := doc[*k8s.KubeNodeConfigV1Alpha1](t, cfg).LabelsConfig; l["kubit.dev/data-disks"] != "2" {
		t.Errorf("labels = %v", l)
	}
	if hasDoc[*block.UserVolumeConfigV1Alpha1](load(t, g.Nodes["cp-01"])) {
		t.Error("cp-01 declares no data disks")
	}
}

func TestValidateDataDisks(t *testing.T) {
	for _, bad := range []string{"dataDisks: [/dev/vda]", "dataDisks: [/dev/vdb, /dev/vdb]", "dataDisks: ['']"} {
		y := strings.Replace(sampleCluster, "dataDisks: [/dev/vdb, /dev/vdc]", bad, 1)
		if _, err := config.Parse([]byte(y)); err == nil {
			t.Errorf("%s must be rejected", bad)
		}
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
	if hasDoc[*network.Layer2VIPConfigV1Alpha1](worker) {
		t.Error("workers must not carry the VIP")
	}
	if !hasDoc[*network.DHCPv4ConfigV1Alpha1](worker) || !hasDoc[*network.LinkAliasConfigV1Alpha1](worker) {
		t.Error("a node without static config must declare DHCP on the uplink alias explicitly")
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
	g, err := config.Generate(c, nil, config.FixedInstaller(installer))
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
	g, err = config.Generate(c, nil, config.FixedInstaller(installer))
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
	g2, err := config.Generate(c, g1.Secrets, config.FixedInstaller(installer))
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
	g4, err := config.Generate(c, restored, config.FixedInstaller(installer))
	if err != nil {
		t.Fatalf("generate from restored bundle: %v", err)
	}
	if g4.Secrets.Cluster.ID != g1.Secrets.Cluster.ID || g4.Talosconfig == nil {
		t.Error("restored bundle must keep identity and produce a talosconfig")
	}
}

const pooledCluster = `
apiVersion: kubit.dev/v1
kind: Cluster
metadata: { name: pooled }
spec:
  network:
    nameservers: [192.168.64.1, 1.1.1.1]
    ntp: [pool.ntp.org]
  pools:
    - name: controlplane
      role: controlplane
    - name: worker
      role: worker
    - name: gpu
      role: worker
      labels: { workload: gpu }
      taints: { nvidia.com/gpu: "true:NoSchedule" }
      extensions: [siderolabs/nvidia-open-gpu-kernel-modules-lts]
      schematicID: gpu-schematic
      installDisk: { selector: { minSize: 100GB, type: nvme } }
  nodes:
    - { hostname: cp-01, ip: 192.168.64.2, mac: "52:54:00:4b:49:01", pool: controlplane, installDisk: { path: /dev/vda } }
    - { hostname: gpu-01, ip: 192.168.64.3, mac: "52:54:00:4b:49:02", pool: gpu, kvm: true, labels: { rack: a1 },
        network: { addresses: [192.168.64.150/24], gateway: 192.168.64.1, nameservers: [9.9.9.9], vlan: 0, mtu: 1500 } }
    - { hostname: vlan-01, ip: 192.168.64.4, mac: "52:54:00:4b:49:03", pool: worker, installDisk: { path: /dev/vda },
        network: { addresses: [10.20.0.5/24], gateway: 10.20.0.1, vlan: 20 } }
`

func TestPoolsResolveRoleDiskAndLabels(t *testing.T) {
	c, err := config.Parse([]byte(pooledCluster))
	if err != nil {
		t.Fatal(err)
	}
	gpu := c.Spec.Nodes[1]
	if gpu.Role != config.RoleWorker || gpu.InstallDisk.Selector == nil || gpu.InstallDisk.Selector.Type != "nvme" {
		t.Errorf("pool defaults not applied: %+v", gpu)
	}
	if l := c.NodeLabels(gpu); l["workload"] != "gpu" || l["rack"] != "a1" {
		t.Errorf("labels = %v", l)
	}
	if len(c.ControlPlanes()) != 1 || len(c.Workers()) != 2 {
		t.Errorf("roles via pools: %d cp %d workers", len(c.ControlPlanes()), len(c.Workers()))
	}
	if c.SchematicFor(c.PoolOf(gpu)) != "gpu-schematic" || c.SchematicFor(c.PoolOf(c.Spec.Nodes[0])) != "" {
		t.Error("pool schematic resolution")
	}
	installer := func(p config.Pool) string { return "img:" + p.Name }
	g, err := config.Generate(c, nil, installer)
	if err != nil {
		t.Fatal(err)
	}
	gpuCfg := load(t, g.Nodes["gpu-01"])
	if img := doc[*runtime.UnattendedInstallConfigV1Alpha1](t, gpuCfg).Installer.Image; img != "img:gpu" {
		t.Errorf("gpu pool must install from its own image, got %s", img)
	}
	node := doc[*k8s.KubeNodeConfigV1Alpha1](t, gpuCfg)
	if node.LabelsConfig["workload"] != "gpu" || node.LabelsConfig["rack"] != "a1" || node.LabelsConfig["kubit.dev/pool"] != "gpu" {
		t.Errorf("labels: %v", node.LabelsConfig)
	}
	if node.TaintsConfig["nvidia.com/gpu"] != "true:NoSchedule" {
		t.Errorf("taints: %v", node.TaintsConfig)
	}
	link := doc[*network.LinkConfigV1Alpha1](t, gpuCfg)
	if link.Name() != "uplink" || len(link.LinkAddresses) != 1 || link.LinkAddresses[0].AddressAddress.String() != "192.168.64.150/24" {
		t.Errorf("static link: %+v", link)
	}
	if len(link.LinkRoutes) != 1 || link.LinkRoutes[0].RouteGateway.String() != "192.168.64.1" {
		t.Errorf("default route: %+v", link.LinkRoutes)
	}
	if hasDoc[*network.DHCPv4ConfigV1Alpha1](gpuCfg) {
		t.Error("static node must not also run DHCP on the uplink")
	}
	res := doc[*network.ResolverConfigV1Alpha1](t, gpuCfg)
	if len(res.ResolverNameservers) != 1 || res.ResolverNameservers[0].Address.String() != "9.9.9.9" {
		t.Errorf("node nameservers override the cluster's: %+v", res.ResolverNameservers)
	}
	cp := load(t, g.Nodes["cp-01"])
	if r := doc[*network.ResolverConfigV1Alpha1](t, cp); len(r.ResolverNameservers) != 2 {
		t.Errorf("cluster nameservers: %+v", r.ResolverNameservers)
	}
	if ts := doc[*network.TimeSyncConfigV1Alpha1](t, cp); ts.TimeNTP == nil || ts.TimeNTP.Servers[0] != "pool.ntp.org" {
		t.Errorf("ntp: %+v", ts)
	}
	vlanCfg := load(t, g.Nodes["vlan-01"])
	vlan := doc[*network.VLANConfigV1Alpha1](t, vlanCfg)
	if vlan.VLANIDConfig != 20 || vlan.ParentLinkConfig != "uplink" || len(vlan.LinkAddresses) != 1 {
		t.Errorf("vlan: %+v", vlan)
	}
}

func TestValidateRejectsPoolMistakes(t *testing.T) {
	cases := map[string]string{
		"unknown pool":      strings.Replace(pooledCluster, "pool: gpu,", "pool: nope,", 1),
		"two cp pools":      strings.Replace(pooledCluster, "- name: worker\n      role: worker", "- name: worker\n      role: controlplane", 1),
		"bad taint effect":  strings.Replace(pooledCluster, `"true:NoSchedule"`, `"true:Sometimes"`, 1),
		"duplicate mac":     strings.Replace(pooledCluster, "52:54:00:4b:49:02", "52:54:00:4b:49:01", 1),
		"address not cidr":  strings.Replace(pooledCluster, "192.168.64.150/24", "192.168.64.150", 1),
		"address is vip":    strings.Replace(pooledCluster, "spec:\n  network:", "spec:\n  controlPlane: { vip: 192.168.64.150 }\n  network:", 1),
		"vlan out of range": strings.Replace(pooledCluster, "vlan: 20", "vlan: 5000", 1),
	}
	for name, doc := range cases {
		if _, err := config.Parse([]byte(doc)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestLegacyRoleDeclarationsStillRender(t *testing.T) {
	c, err := config.Parse([]byte(sampleCluster))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Spec.Pools) != 2 || c.Spec.Nodes[0].Pool != "controlplane" || c.Spec.Nodes[3].Pool != "worker" {
		t.Errorf("default pools: %+v / %s %s", c.Spec.Pools, c.Spec.Nodes[0].Pool, c.Spec.Nodes[3].Pool)
	}
}
