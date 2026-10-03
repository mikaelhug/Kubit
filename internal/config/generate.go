package config

import (
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"net/netip"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/cel"
	"github.com/siderolabs/talos/pkg/machinery/cel/celenv"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	talosconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/config"
	"github.com/siderolabs/talos/pkg/machinery/config/container"
	"github.com/siderolabs/talos/pkg/machinery/config/generate"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/config/machine"
	"github.com/siderolabs/talos/pkg/machinery/config/types/block"
	"github.com/siderolabs/talos/pkg/machinery/config/types/cri"
	"github.com/siderolabs/talos/pkg/machinery/config/types/k8s"
	"github.com/siderolabs/talos/pkg/machinery/config/types/meta"
	"github.com/siderolabs/talos/pkg/machinery/config/types/network"
	"github.com/siderolabs/talos/pkg/machinery/config/types/runtime"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	blockres "github.com/siderolabs/talos/pkg/machinery/resources/block"
	"go.yaml.in/yaml/v4"
)

const (
	gvisorUserNamespacesSysctl = "user.max_user_namespaces"
	gvisorUserNamespacesValue  = "11255"

	LabelGVisor             = "sandbox.runtime/gvisor"
	LabelGVisorKVM          = "sandbox.runtime/gvisor-kvm"
	LabelPool               = "kubit.dev/pool"
	LabelDataDisks          = "kubit.dev/data-disks"
	LabelLonghornDisk       = "node.longhorn.io/create-default-disk"
	AnnotationLonghornDisks = "node.longhorn.io/default-disks-config"

	vipLinkAlias = "uplink"
)

type Generated struct {
	Secrets     *secrets.Bundle
	Talosconfig *clientconfig.Config
	Nodes       map[string][]byte
}

type Installer func(p Pool) string

func FixedInstaller(image string) Installer { return func(Pool) string { return image } }

func Generate(c *Cluster, bundle *secrets.Bundle, installer Installer) (*Generated, error) {
	contract, err := talosconfig.ParseContractFromVersion(c.Spec.TalosVersion)
	if err != nil {
		return nil, fmt.Errorf("talosVersion: %w", err)
	}
	if bundle == nil {
		bundle, err = secrets.NewBundle(secrets.NewFixedClock(time.Now()), contract)
		if err != nil {
			return nil, err
		}
	}
	var cpIPs []string
	for _, n := range c.ControlPlanes() {
		cpIPs = append(cpIPs, n.IP)
	}
	opts := []generate.Option{
		generate.WithVersionContract(contract),
		generate.WithSecretsBundle(bundle),
		generate.WithEndpointList(cpIPs),
		generate.WithInstallImage(installer(c.controlPlaneInstallPool())),
		generate.WithAllowSchedulingOnControlPlanes(*c.Spec.ControlPlane.AllowScheduling),
		generate.WithSkipUnattendedInstallConfig(true),
		generate.WithClusterDiscovery(c.DiscoveryOn()),
	}
	if c.Spec.Platform.GVisor.Enabled {
		opts = append(opts, generate.WithSysctls(map[string]string{gvisorUserNamespacesSysctl: gvisorUserNamespacesValue}))
	}
	if sans := c.apiServerSANs(cpIPs); len(sans) > 0 {
		opts = append(opts, generate.WithAdditionalSubjectAltNames(sans))
	}
	in, err := generate.NewInput(c.Metadata.Name, c.Spec.ControlPlane.Endpoint, strings.TrimPrefix(c.Spec.KubernetesVersion, "v"), opts...)
	if err != nil {
		return nil, err
	}
	in.PodNet = []string{c.Spec.Network.PodCIDR}
	in.ServiceNet = []string{c.Spec.Network.ServiceCIDR}

	out := &Generated{Secrets: bundle, Nodes: make(map[string][]byte, len(c.Spec.Nodes))}
	for _, n := range c.Spec.Nodes {
		b, err := generateNode(c, in, n, installer(c.PoolOf(n)))
		if err != nil {
			return nil, fmt.Errorf("node %s: %w", n.Hostname, err)
		}
		out.Nodes[n.Hostname] = b
	}
	if out.Talosconfig, err = in.Talosconfig(); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Cluster) apiServerSANs(cpIPs []string) []string {
	var sans []string
	if vip := c.Spec.ControlPlane.VIP; vip != "" {
		sans = append(sans, vip)
	}
	if u, err := url.Parse(c.Spec.ControlPlane.Endpoint); err == nil {
		if host := u.Hostname(); host != "" && !slices.Contains(sans, host) && !slices.Contains(cpIPs, host) {
			sans = append(sans, host)
		}
	}
	return sans
}

func (c *Cluster) controlPlaneInstallPool() Pool {
	if p := c.ControlPlanePool(); p != nil {
		return *p
	}
	return Pool{Name: string(RoleControlPlane), Role: RoleControlPlane}
}

func generateNode(c *Cluster, in *generate.Input, n Node, installerImage string) ([]byte, error) {
	mt := machine.TypeWorker
	if n.Role == RoleControlPlane {
		mt = machine.TypeControlPlane
	}
	base, err := in.Config(mt)
	if err != nil {
		return nil, err
	}
	docs := base.Documents()
	if authn := c.Spec.Auth.AuthenticationConfig(); authn != nil && n.Role == RoleControlPlane {
		auth := findOrAppend(&docs, k8s.DefaultAuthenticationConfig)
		auth.AuthConfig = meta.Unstructured{Object: withJWT(auth.AuthConfig.Object, authn["jwt"])}
	}
	if c.PoliciesOn() {
		for _, d := range docs {
			if flannel, ok := d.(*k8s.KubeFlannelCNIConfigV1Alpha1); ok {
				flannel.FlannelKubeNetworkPoliciesEnabled = new(true)
			}
		}
	}

	install := runtime.NewUnattendedInstallConfigV1Alpha1()
	install.Installer.Image = installerImage
	install.ProvisioningSpec.Wipe = new(false)
	if install.ProvisioningSpec.DiskSelector.Match, err = diskSelector(n.InstallDisk); err != nil {
		return nil, err
	}
	docs = append(docs, install)
	for i, path := range n.DataDisks {
		vol, err := dataVolume(i+1, path)
		if err != nil {
			return nil, err
		}
		docs = append(docs, vol)
	}
	if c.SharesSystemDisk(n) {
		if _, err := c.Spec.Storage.EphemeralBytes(); err != nil {
			return nil, err
		}
		eph := volumeConfig(&docs, constants.EphemeralPartitionLabel)
		eph.ProvisioningSpec.ProvisioningMaxSize = block.MustSize(c.Spec.Storage.EphemeralSize)
		eph.ProvisioningSpec.ProvisioningGrow = new(false)
		vol, err := systemVolume()
		if err != nil {
			return nil, err
		}
		docs = append(docs, vol)
	}
	encryptVolumes(&docs, c.Spec.Storage)
	if n.Watchdog {
		docs = append(docs, watchdogTimer())
	}

	host := findOrAppend(&docs, network.NewHostnameConfigV1Alpha1)
	host.ConfigAuto = nil
	host.ConfigHostname = n.Hostname

	node := findOrAppend(&docs, k8s.NewKubeNodeConfigV1Alpha1)
	orEmpty(&node.LabelsConfig)
	if c.Spec.Platform.GVisor.Enabled {
		node.LabelsConfig[LabelGVisor] = "true"
		if n.KVM {
			node.LabelsConfig[LabelGVisorKVM] = "true"
		}
	}
	node.LabelsConfig[LabelPool] = n.Pool
	if len(n.DataDisks) > 0 {
		node.LabelsConfig[LabelDataDisks] = fmt.Sprint(len(n.DataDisks))
	}
	if c.Spec.Platform.Longhorn.Enabled {
		if mounts := c.StorageMounts(n); len(mounts) > 0 {
			node.LabelsConfig[LabelLonghornDisk] = "config"
			orEmpty(&node.AnnotationsConfig)[AnnotationLonghornDisks] = longhornDisksConfig(mounts)
		} else {
			node.LabelsConfig[LabelLonghornDisk] = "false"
		}
	}
	maps.Copy(node.LabelsConfig, c.NodeLabels(n))
	if taints := c.NodeTaints(n); len(taints) > 0 {
		maps.Copy(orEmpty(&node.TaintsConfig), taints)
	}
	if ann := c.NodeAnnotations(n); len(ann) > 0 {
		maps.Copy(orEmpty(&node.AnnotationsConfig), ann)
	}
	if n.Role == RoleControlPlane && *c.Spec.ControlPlane.AllowScheduling {
		delete(node.LabelsConfig, constants.LabelExcludeFromExternalLB)
	}

	alias := network.NewLinkAliasConfigV1Alpha1(vipLinkAlias)
	if alias.Selector.Match, err = uplinkSelector(n.MAC); err != nil {
		return nil, err
	}
	docs = append(docs, alias)
	linkName := vipLinkAlias
	if n.Network != nil && n.Network.VLAN > 0 {
		vlan := network.NewVLANConfigV1Alpha1(fmt.Sprintf("%s.%d", vipLinkAlias, n.Network.VLAN))
		vlan.VLANIDConfig = n.Network.VLAN
		vlan.ParentLinkConfig = vipLinkAlias
		linkName = vlan.MetaName
		if err := fillLink(&vlan.CommonLinkConfig, n.Network); err != nil {
			return nil, err
		}
		docs = append(docs, vlan)
	} else if n.Network != nil {
		link := network.NewLinkConfigV1Alpha1(vipLinkAlias)
		if err := fillLink(&link.CommonLinkConfig, n.Network); err != nil {
			return nil, err
		}
		docs = append(docs, link)
	} else {
		docs = append(docs, network.NewDHCPv4ConfigV1Alpha1(vipLinkAlias))
	}
	if vip := c.Spec.ControlPlane.VIP; vip != "" && n.Role == RoleControlPlane {
		v := network.NewLayer2VIPConfigV1Alpha1(vip)
		v.LinkName = linkName
		docs = append(docs, v)
	}
	if c.Spec.Platform.Builds.Enabled {
		mirror, err := registryMirror(c.RegistryIP())
		if err != nil {
			return nil, err
		}
		docs = append(docs, mirror)
	}
	if nameservers := firstNonEmpty(nodeNameservers(n), c.Spec.Network.Nameservers); len(nameservers) > 0 {
		resolver := findOrAppend(&docs, network.NewResolverConfigV1Alpha1)
		resolver.ResolverNameservers = nil
		for _, ns := range nameservers {
			addr, err := netip.ParseAddr(ns)
			if err != nil {
				return nil, fmt.Errorf("nameserver %q: %w", ns, err)
			}
			resolver.ResolverNameservers = append(resolver.ResolverNameservers, network.NameserverConfig{Address: meta.Addr{Addr: addr}})
		}
	}
	if len(c.Spec.Network.NTP) > 0 {
		ts := findOrAppend(&docs, network.NewTimeSyncConfigV1Alpha1)
		ts.TimeNTP = &network.NTPConfig{Servers: c.Spec.Network.NTP}
	}

	docs = append(docs, firewallDocs(c, n)...)

	cfg, err := container.New(docs...)
	if err != nil {
		return nil, err
	}
	patched, err := applyPatches(cfg, nodePatches(c, n))
	if err != nil {
		return nil, err
	}
	return patched.Bytes()
}

func withJWT(authn map[string]any, jwt any) map[string]any {
	out := maps.Clone(authn)
	if out == nil {
		out = map[string]any{}
	}
	existing, _ := out["jwt"].([]any)
	added, _ := jwt.([]any)
	out["jwt"] = append(slices.Clone(existing), added...)
	return out
}

func fillLink(l *network.CommonLinkConfig, nn *NodeNetwork) error {
	up := true
	l.LinkUp = &up
	if nn.MTU > 0 {
		l.LinkMTU = nn.MTU
	}
	for _, a := range nn.Addresses {
		pfx, err := netip.ParsePrefix(a)
		if err != nil {
			return fmt.Errorf("address %q: %w", a, err)
		}
		l.LinkAddresses = append(l.LinkAddresses, network.AddressConfig{AddressAddress: pfx})
	}
	if nn.Gateway != "" {
		gw, err := netip.ParseAddr(nn.Gateway)
		if err != nil {
			return fmt.Errorf("gateway: %w", err)
		}
		l.LinkRoutes = append(l.LinkRoutes, network.RouteConfig{RouteGateway: meta.Addr{Addr: gw}})
	}
	return nil
}

func nodeNameservers(n Node) []string {
	if n.Network == nil {
		return nil
	}
	return n.Network.Nameservers
}

func firstNonEmpty(a, b []string) []string {
	if len(a) > 0 {
		return a
	}
	return b
}

func findOrAppend[T config.Document](docs *[]config.Document, newDoc func() T) T {
	for _, d := range *docs {
		if t, ok := d.(T); ok {
			return t
		}
	}
	t := newDoc()
	*docs = append(*docs, t)
	return t
}

func uplinkSelector(mac string) (cel.Expression, error) {
	expr := `link.type == 1 && link.kind == ""`
	if mac != "" {
		expr = fmt.Sprintf("mac(link.permanent_addr) == %q", strings.ToLower(mac))
	}
	return cel.ParseBooleanExpression(expr, celenv.LinkLocator())
}

func DataMount(n int) string { return fmt.Sprintf("/var/mnt/data-%d", n) }

func (c *Cluster) StorageMounts(n Node) []string {
	var out []string
	for i := range n.DataDisks {
		out = append(out, DataMount(i+1))
	}
	if c.SharesSystemDisk(n) {
		out = append(out, "/var/mnt/"+SystemDataVolume)
	}
	return out
}

func orEmpty(m *map[string]string) map[string]string {
	if *m == nil {
		*m = map[string]string{}
	}
	return *m
}

func userVolume(name string, typ blockres.VolumeType, match cel.Expression) *block.UserVolumeConfigV1Alpha1 {
	vol := block.NewUserVolumeConfigV1Alpha1()
	vol.MetaName = name
	vol.VolumeType = &typ
	vol.ProvisioningSpec.DiskSelectorSpec.Match = match
	vol.FilesystemSpec.FilesystemType = blockres.FilesystemTypeXFS
	return vol
}

func dataVolume(n int, path string) (*block.UserVolumeConfigV1Alpha1, error) {
	match, err := diskSelector(InstallDisk{Path: path})
	if err != nil {
		return nil, err
	}
	return userVolume(fmt.Sprintf("data-%d", n), blockres.VolumeTypeDisk, match), nil
}

func registryMirror(ip string) (*cri.RegistryMirrorConfigV1Alpha1, error) {
	u, err := url.Parse("http://" + net.JoinHostPort(ip, fmt.Sprint(RegistryPort)))
	if err != nil {
		return nil, err
	}
	m := cri.NewRegistryMirrorConfigV1Alpha1(RegistryHost)
	m.RegistryEndpoints = []cri.RegistryEndpoint{{EndpointURL: meta.URL{URL: u}}}
	m.RegistrySkipFallback = new(true)
	return m, nil
}

func systemVolume() (*block.UserVolumeConfigV1Alpha1, error) {
	match, err := cel.ParseBooleanExpression("system_disk", celenv.DiskLocator())
	if err != nil {
		return nil, err
	}
	vol := userVolume(SystemDataVolume, blockres.VolumeTypePartition, match)
	vol.ProvisioningSpec.ProvisioningMinSize = block.MustByteSize(MinSystemDataSize)
	vol.ProvisioningSpec.ProvisioningGrow = new(true)
	return vol, nil
}

func volumeConfig(docs *[]config.Document, name string) *block.VolumeConfigV1Alpha1 {
	for _, d := range *docs {
		if v, ok := d.(*block.VolumeConfigV1Alpha1); ok && v.MetaName == name {
			return v
		}
	}
	v := block.NewVolumeConfigV1Alpha1()
	v.MetaName = name
	*docs = append(*docs, v)
	return v
}

func encryptVolumes(docs *[]config.Document, s Storage) {
	spec, ok := s.encryptionSpec()
	if !ok {
		return
	}
	volumeConfig(docs, constants.StatePartitionLabel).EncryptionSpec = spec
	volumeConfig(docs, constants.EphemeralPartitionLabel).EncryptionSpec = spec
	for _, d := range *docs {
		if v, ok := d.(*block.UserVolumeConfigV1Alpha1); ok {
			v.EncryptionSpec = spec
		}
	}
}

const watchdogDevice = "/dev/watchdog0"

func watchdogTimer() *runtime.WatchdogTimerV1Alpha1 {
	wd := runtime.NewWatchdogTimerV1Alpha1()
	wd.WatchdogDevice = watchdogDevice
	wd.WatchdogTimeout = runtime.DefaultWatchdogTimeout
	return wd
}

func diskSelector(d InstallDisk) (cel.Expression, error) {
	var terms []string
	switch {
	case d.Path != "":
		terms = append(terms, fmt.Sprintf("disk.dev_path == %q", d.Path))
	case d.Selector != nil:
		if d.Selector.MinSize != "" {
			terms = append(terms, "disk.size >= "+celSize(d.Selector.MinSize))
		}
		if d.Selector.Type != "" {
			terms = append(terms, fmt.Sprintf("disk.transport == %q", d.Selector.Type))
		}
		if d.Selector.Model != "" {
			terms = append(terms, fmt.Sprintf("glob(%q, disk.model)", d.Selector.Model))
		}
		terms = append(terms, "!disk.readonly", "!disk.cdrom")
	default:
		return cel.Expression{}, fmt.Errorf("installDisk: path or selector required")
	}
	return cel.ParseBooleanExpression(strings.Join(terms, " && "), celenv.DiskLocator())
}

func celSize(s string) string {
	s = strings.TrimSpace(s)
	i := len(s)
	for i > 0 && (s[i-1] < '0' || s[i-1] > '9') {
		i--
	}
	num, unit := s[:i], strings.ToUpper(strings.TrimSpace(s[i:]))
	if unit == "" {
		return num + "u"
	}
	return num + "u * " + unit
}

func ParseSecrets(b []byte) (*secrets.Bundle, error) {
	var bundle secrets.Bundle
	if err := yaml.Unmarshal(b, &bundle); err != nil {
		return nil, fmt.Errorf("secrets bundle: %w", err)
	}
	bundle.Clock = secrets.NewClock()
	return &bundle, nil
}

func longhornDisksConfig(mounts []string) string {
	disks := make([]map[string]any, 0, len(mounts))
	for _, m := range mounts {
		disks = append(disks, map[string]any{"path": m, "allowScheduling": true, "name": path.Base(m)})
	}
	b, _ := json.Marshal(disks)
	return string(b)
}
