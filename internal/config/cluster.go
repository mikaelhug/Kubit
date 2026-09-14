// Package config defines cluster.yaml, the single declarative input to Kubit, and
// turns it into Talos machine configuration.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strings"

	talosconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	"github.com/siderolabs/talos/pkg/machinery/gendata"
	"go.yaml.in/yaml/v4"
)

const (
	// MinTalosVersion is the oldest release whose config document set Kubit generates.
	MinTalosVersion = "v1.14.0"

	APIVersion  = "kubit.dev/v1"
	KindCluster = "Cluster"

	RoleControlPlane Role = "controlplane"
	RoleWorker       Role = "worker"

	ArchAMD64 Arch = "amd64"
	ArchARM64 Arch = "arm64"
)

type (
	Role string
	Arch string
)

type Cluster struct {
	APIVersion string   `yaml:"apiVersion" json:"apiVersion"`
	Kind       string   `yaml:"kind" json:"kind"`
	Metadata   Metadata `yaml:"metadata" json:"metadata"`
	Spec       Spec     `yaml:"spec" json:"spec"`
}

type Metadata struct {
	Name string `yaml:"name" json:"name"`
}

type Spec struct {
	TalosVersion      string       `yaml:"talosVersion,omitempty" json:"talosVersion,omitempty"`
	KubernetesVersion string       `yaml:"kubernetesVersion,omitempty" json:"kubernetesVersion,omitempty"`
	Extensions        []string     `yaml:"extensions,omitempty" json:"extensions,omitempty"`
	SchematicID       string       `yaml:"schematicID,omitempty" json:"schematicID,omitempty"`
	ControlPlane      ControlPlane `yaml:"controlPlane" json:"controlPlane"`
	Network           Network      `yaml:"network" json:"network"`
	Nodes             []Node       `yaml:"nodes" json:"nodes"`
	Platform          Platform     `yaml:"platform" json:"platform"`
}

type ControlPlane struct {
	// Endpoint is the Kubernetes API URL clients use; defaults to the VIP, else the first control plane IP.
	Endpoint        string `yaml:"endpoint,omitempty" json:"endpoint,omitempty"`
	VIP             string `yaml:"vip,omitempty" json:"vip,omitempty"`
	AllowScheduling *bool  `yaml:"allowScheduling,omitempty" json:"allowScheduling,omitempty"`
}

type Network struct {
	PodCIDR     string `yaml:"podCIDR,omitempty" json:"podCIDR,omitempty"`
	ServiceCIDR string `yaml:"serviceCIDR,omitempty" json:"serviceCIDR,omitempty"`
}

type Node struct {
	Hostname string `yaml:"hostname" json:"hostname"`
	IP       string `yaml:"ip" json:"ip"`
	// MAC of the uplink; filled by discovery. Selects the VIP interface on multi-NIC hosts.
	MAC         string      `yaml:"mac,omitempty" json:"mac,omitempty"`
	Role        Role        `yaml:"role" json:"role"`
	Arch        Arch        `yaml:"arch,omitempty" json:"arch,omitempty"`
	InstallDisk InstallDisk `yaml:"installDisk" json:"installDisk"`
	// KVM marks nodes where /dev/kvm exists, enabling the runsc-kvm RuntimeClass.
	KVM bool `yaml:"kvm,omitempty" json:"kvm,omitempty"`
}

// InstallDisk selects the target disk either by explicit device path or by a selector;
// exactly one of the two must be set.
type InstallDisk struct {
	Path     string        `yaml:"path,omitempty" json:"path,omitempty"`
	Selector *DiskSelector `yaml:"selector,omitempty" json:"selector,omitempty"`
}

type DiskSelector struct {
	// MinSize like "100GB"; Type like "nvme", "sata", "virtio"; Model is a glob.
	MinSize string `yaml:"minSize,omitempty" json:"minSize,omitempty"`
	Type    string `yaml:"type,omitempty" json:"type,omitempty"`
	Model   string `yaml:"model,omitempty" json:"model,omitempty"`
}

type Platform struct {
	MetalLB       MetalLB `yaml:"metallb" json:"metallb"`
	IngressNginx  Addon   `yaml:"ingressNginx" json:"ingressNginx"`
	GVisor        Addon   `yaml:"gvisor" json:"gvisor"`
	MetricsServer Addon   `yaml:"metricsServer" json:"metricsServer"`
	CertManager   Addon   `yaml:"certManager" json:"certManager"`
	ArgoCD        Addon   `yaml:"argocd" json:"argocd"`
}

type Addon struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
	// Values are merged into the add-on's Helm chart values (free-form).
	Values map[string]any `yaml:"values,omitempty" json:"values,omitempty"`
}

type MetalLB struct {
	Enabled bool           `yaml:"enabled" json:"enabled"`
	Range   string         `yaml:"range,omitempty" json:"range,omitempty"` // "a.b.c.d-a.b.c.e"
	Values  map[string]any `yaml:"values,omitempty" json:"values,omitempty"`
}

func Load(path string) (*Cluster, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

func Parse(b []byte) (*Cluster, error) {
	var c Cluster
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse cluster.yaml: %w", err)
	}
	c.applyDefaults()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Cluster) Marshal() ([]byte, error) { return yaml.Marshal(c) }

func (c *Cluster) Save(path string) error {
	b, err := c.Marshal()
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

func (c *Cluster) applyDefaults() {
	if c.Spec.TalosVersion == "" {
		c.Spec.TalosVersion = gendata.VersionTag
	}
	if c.Spec.KubernetesVersion == "" {
		c.Spec.KubernetesVersion = "v" + constants.DefaultKubernetesVersion
	}
	if c.Spec.Network.PodCIDR == "" {
		c.Spec.Network.PodCIDR = constants.DefaultIPv4PodCIDR
	}
	if c.Spec.Network.ServiceCIDR == "" {
		c.Spec.Network.ServiceCIDR = constants.DefaultIPv4ServiceCIDR
	}
	if c.Spec.Platform.GVisor.Enabled && !containsString(c.Spec.Extensions, "siderolabs/gvisor") {
		c.Spec.Extensions = append(c.Spec.Extensions, "siderolabs/gvisor")
	}
	if c.Spec.ControlPlane.AllowScheduling == nil {
		v := len(c.Spec.Nodes) < 6
		c.Spec.ControlPlane.AllowScheduling = &v
	}
	for i := range c.Spec.Nodes {
		if c.Spec.Nodes[i].Arch == "" {
			c.Spec.Nodes[i].Arch = ArchAMD64
		}
	}
	if c.Spec.ControlPlane.Endpoint == "" {
		host := c.Spec.ControlPlane.VIP
		if host == "" {
			if cps := c.ControlPlanes(); len(cps) > 0 {
				host = cps[0].IP
			}
		}
		if host != "" {
			c.Spec.ControlPlane.Endpoint = "https://" + net.JoinHostPort(host, "6443")
		}
	}
}

var hostnameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func (c *Cluster) Validate() error {
	var errs []error
	if c.APIVersion != APIVersion || c.Kind != KindCluster {
		errs = append(errs, fmt.Errorf("expected apiVersion %s kind %s", APIVersion, KindCluster))
	}
	if contract, err := talosconfig.ParseContractFromVersion(c.Spec.TalosVersion); err != nil {
		errs = append(errs, fmt.Errorf("talosVersion: %w", err))
	} else if !contract.UnattendedInstallConfig() || !contract.MultidocKubernetesConfigSupported() {
		// Kubit emits only the multi-document form (UnattendedInstallConfig, KubeNodeConfig,
		// HostnameConfig, ...), which older Talos releases do not register.
		errs = append(errs, fmt.Errorf("talosVersion %s: Kubit requires Talos %s or newer", c.Spec.TalosVersion, MinTalosVersion))
	}
	if !hostnameRE.MatchString(c.Metadata.Name) {
		errs = append(errs, fmt.Errorf("metadata.name %q must be a DNS label", c.Metadata.Name))
	}
	if len(c.Spec.Nodes) == 0 {
		errs = append(errs, errors.New("spec.nodes must not be empty"))
	}
	if len(c.ControlPlanes()) == 0 {
		errs = append(errs, errors.New("at least one controlplane node is required"))
	}
	if u, err := url.Parse(c.Spec.ControlPlane.Endpoint); err != nil || u.Scheme != "https" || u.Host == "" {
		errs = append(errs, fmt.Errorf("controlPlane.endpoint %q must be an https URL", c.Spec.ControlPlane.Endpoint))
	}
	if v := c.Spec.ControlPlane.VIP; v != "" {
		if _, err := netip.ParseAddr(v); err != nil {
			errs = append(errs, fmt.Errorf("controlPlane.vip: %w", err))
		}
	}
	for _, cidr := range []string{c.Spec.Network.PodCIDR, c.Spec.Network.ServiceCIDR} {
		if _, err := netip.ParsePrefix(cidr); err != nil {
			errs = append(errs, fmt.Errorf("network CIDR %q: %w", cidr, err))
		}
	}
	seenHost, seenIP := map[string]bool{}, map[string]bool{}
	for i, n := range c.Spec.Nodes {
		p := fmt.Sprintf("nodes[%d]", i)
		if !hostnameRE.MatchString(n.Hostname) {
			errs = append(errs, fmt.Errorf("%s.hostname %q must be a DNS label", p, n.Hostname))
		}
		if seenHost[n.Hostname] {
			errs = append(errs, fmt.Errorf("%s.hostname %q duplicated", p, n.Hostname))
		}
		seenHost[n.Hostname] = true
		if _, err := netip.ParseAddr(n.IP); err != nil {
			errs = append(errs, fmt.Errorf("%s.ip: %w", p, err))
		}
		if n.MAC != "" {
			if _, err := net.ParseMAC(n.MAC); err != nil {
				errs = append(errs, fmt.Errorf("%s.mac: %w", p, err))
			}
		}
		if seenIP[n.IP] {
			errs = append(errs, fmt.Errorf("%s.ip %q duplicated", p, n.IP))
		}
		seenIP[n.IP] = true
		if n.Role != RoleControlPlane && n.Role != RoleWorker {
			errs = append(errs, fmt.Errorf("%s.role %q must be controlplane or worker", p, n.Role))
		}
		if n.Arch != ArchAMD64 && n.Arch != ArchARM64 {
			errs = append(errs, fmt.Errorf("%s.arch %q must be amd64 or arm64", p, n.Arch))
		}
		if (n.InstallDisk.Path == "") == (n.InstallDisk.Selector == nil) {
			errs = append(errs, fmt.Errorf("%s.installDisk needs exactly one of path or selector", p))
		}
	}
	if m := c.Spec.Platform.MetalLB; m.Enabled {
		if _, _, err := ParseIPRange(m.Range); err != nil {
			errs = append(errs, fmt.Errorf("platform.metallb.range: %w", err))
		}
	}
	return errors.Join(errs...)
}

func (c *Cluster) ControlPlanes() []Node { return c.nodesWithRole(RoleControlPlane) }
func (c *Cluster) Workers() []Node       { return c.nodesWithRole(RoleWorker) }

func (c *Cluster) nodesWithRole(r Role) []Node {
	var out []Node
	for _, n := range c.Spec.Nodes {
		if n.Role == r {
			out = append(out, n)
		}
	}
	return out
}

// ParseIPRange parses "a.b.c.d-a.b.c.e" as used by MetalLB pools.
func ParseIPRange(s string) (netip.Addr, netip.Addr, error) {
	from, to, ok := strings.Cut(s, "-")
	if !ok {
		return netip.Addr{}, netip.Addr{}, fmt.Errorf("%q is not of the form start-end", s)
	}
	a, err := netip.ParseAddr(strings.TrimSpace(from))
	if err != nil {
		return netip.Addr{}, netip.Addr{}, err
	}
	b, err := netip.ParseAddr(strings.TrimSpace(to))
	if err != nil {
		return netip.Addr{}, netip.Addr{}, err
	}
	if b.Less(a) {
		return netip.Addr{}, netip.Addr{}, fmt.Errorf("%q: end precedes start", s)
	}
	return a, b, nil
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
