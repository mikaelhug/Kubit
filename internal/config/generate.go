package config

import (
	"fmt"
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
	"github.com/siderolabs/talos/pkg/machinery/config/types/k8s"
	"github.com/siderolabs/talos/pkg/machinery/config/types/network"
	"github.com/siderolabs/talos/pkg/machinery/config/types/runtime"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	"go.yaml.in/yaml/v4"
)

const (
	// gVisor needs unprivileged user namespaces; value from the siderolabs/extensions README.
	gvisorUserNamespacesSysctl = "user.max_user_namespaces"
	gvisorUserNamespacesValue  = "11255"

	LabelGVisor    = "sandbox.runtime/gvisor"
	LabelGVisorKVM = "sandbox.runtime/gvisor-kvm"

	// vipLinkAlias names the physical interface the control plane VIP floats on.
	vipLinkAlias = "uplink"
)

type Generated struct {
	Secrets     *secrets.Bundle
	Talosconfig *clientconfig.Config
	// Nodes maps hostname to multi-document machine config YAML.
	Nodes map[string][]byte
}

// Generate renders one machine config per node. Pass a nil bundle to mint new cluster
// secrets; pass the stored bundle when adding nodes to an existing cluster.
func Generate(c *Cluster, bundle *secrets.Bundle, installerImage string) (*Generated, error) {
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
		generate.WithInstallImage(installerImage),
		generate.WithAllowSchedulingOnControlPlanes(*c.Spec.ControlPlane.AllowScheduling),
		generate.WithSkipUnattendedInstallConfig(true),
	}
	if c.Spec.Platform.GVisor.Enabled {
		opts = append(opts, generate.WithSysctls(map[string]string{gvisorUserNamespacesSysctl: gvisorUserNamespacesValue}))
	}
	if vip := c.Spec.ControlPlane.VIP; vip != "" {
		opts = append(opts, generate.WithAdditionalSubjectAltNames([]string{vip}))
	}
	in, err := generate.NewInput(c.Metadata.Name, c.Spec.ControlPlane.Endpoint, strings.TrimPrefix(c.Spec.KubernetesVersion, "v"), opts...)
	if err != nil {
		return nil, err
	}
	in.PodNet = []string{c.Spec.Network.PodCIDR}
	in.ServiceNet = []string{c.Spec.Network.ServiceCIDR}

	out := &Generated{Secrets: bundle, Nodes: make(map[string][]byte, len(c.Spec.Nodes))}
	for _, n := range c.Spec.Nodes {
		b, err := generateNode(c, in, n, installerImage)
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

	install := runtime.NewUnattendedInstallConfigV1Alpha1()
	install.Installer.Image = installerImage
	install.ProvisioningSpec.Wipe = new(false)
	if install.ProvisioningSpec.DiskSelector.Match, err = diskSelector(n.InstallDisk); err != nil {
		return nil, err
	}
	docs = append(docs, install)

	host := findOrAppend(&docs, network.NewHostnameConfigV1Alpha1)
	host.ConfigAuto = nil
	host.ConfigHostname = n.Hostname

	node := findOrAppend(&docs, k8s.NewKubeNodeConfigV1Alpha1)
	if node.LabelsConfig == nil {
		node.LabelsConfig = map[string]string{}
	}
	if c.Spec.Platform.GVisor.Enabled {
		node.LabelsConfig[LabelGVisor] = "true"
		if n.KVM {
			node.LabelsConfig[LabelGVisorKVM] = "true"
		}
	}
	// Talos excludes control planes from external load balancers; MetalLB honours that
	// label and would never announce from a cluster whose control planes also carry
	// workloads (1–5 nodes), leaving every LoadBalancer IP dark.
	if n.Role == RoleControlPlane && *c.Spec.ControlPlane.AllowScheduling {
		delete(node.LabelsConfig, constants.LabelExcludeFromExternalLB)
	}

	if vip := c.Spec.ControlPlane.VIP; vip != "" && n.Role == RoleControlPlane {
		alias := network.NewLinkAliasConfigV1Alpha1(vipLinkAlias)
		if alias.Selector.Match, err = uplinkSelector(n.MAC); err != nil {
			return nil, err
		}
		v := network.NewLayer2VIPConfigV1Alpha1(vip)
		v.LinkName = vipLinkAlias
		docs = append(docs, alias, v)
	}

	cfg, err := container.New(docs...)
	if err != nil {
		return nil, err
	}
	return cfg.Bytes()
}

// findOrAppend returns the generator's existing document of type T, or appends a new one;
// the container rejects duplicate kinds, so per-node tweaks must edit in place.
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

// uplinkSelector picks the interface for the VIP: by permanent MAC when discovery recorded
// one, otherwise the sole physical ethernet link (LinkStatusSpec.Physical: type ether, no kind).
func uplinkSelector(mac string) (cel.Expression, error) {
	expr := `link.type == 1 && link.kind == ""`
	if mac != "" {
		expr = fmt.Sprintf("mac(link.permanent_addr) == %q", strings.ToLower(mac))
	}
	return cel.ParseBooleanExpression(expr, celenv.LinkLocator())
}

// diskSelector builds the CEL expression Talos evaluates against each disk at install.
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

// celSize turns "100GB" into the CEL form "100u * GB" understood by the disk locator env.
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

var _ config.Document = (*runtime.UnattendedInstallConfigV1Alpha1)(nil)

// ParseSecrets restores a secrets bundle from its YAML form (secrets.yaml). The bundle's
// clock is not serialised and must be re-attached before it can sign certificates.
func ParseSecrets(b []byte) (*secrets.Bundle, error) {
	var bundle secrets.Bundle
	if err := yaml.Unmarshal(b, &bundle); err != nil {
		return nil, fmt.Errorf("secrets bundle: %w", err)
	}
	bundle.Clock = secrets.NewClock()
	return &bundle, nil
}
