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
	"slices"
	"strings"
	"time"

	talosconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/types/block"
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
	TalosVersion      string `yaml:"talosVersion,omitempty" json:"talosVersion,omitempty"`
	KubernetesVersion string `yaml:"kubernetesVersion,omitempty" json:"kubernetesVersion,omitempty"`
	// Extensions and SchematicID are the cluster default; a pool may override them.
	Extensions   []string     `yaml:"extensions,omitempty" json:"extensions,omitempty"`
	SchematicID  string       `yaml:"schematicID,omitempty" json:"schematicID,omitempty"`
	ControlPlane ControlPlane `yaml:"controlPlane" json:"controlPlane"`
	Network      Network      `yaml:"network" json:"network"`
	// Pools group nodes that share role, labels, taints, extensions and disk policy.
	// Absent pools are synthesised: "controlplane" and "worker".
	Pools    []Pool   `yaml:"pools,omitempty" json:"pools,omitempty"`
	Nodes    []Node   `yaml:"nodes" json:"nodes"`
	Platform Platform `yaml:"platform" json:"platform"`
	Backup   Backup   `yaml:"backup" json:"backup"`
	// Maintenance gates disruptive operations to a window; empty = anytime.
	Maintenance Maintenance `yaml:"maintenance,omitempty" json:"maintenance,omitempty"`
	// Auth wires the cluster's API server to an OpenID Connect provider so people
	// use kubectl with their own identity and RBAC binds to their groups.
	Auth    ClusterAuth `yaml:"auth,omitempty" json:"auth,omitempty"`
	Storage Storage     `yaml:"storage,omitempty" json:"storage,omitempty"`
}

type Storage struct {
	SystemDisk    bool   `yaml:"systemDisk,omitempty" json:"systemDisk,omitempty"`
	EphemeralSize string `yaml:"ephemeralSize,omitempty" json:"ephemeralSize,omitempty"`
}

const (
	DefaultEphemeralSize = "40GiB"
	MinEphemeralBytes    = 10 << 30
	MinSystemDataSize    = "10GiB"
	SystemDataVolume     = "data-system"
)

func (s Storage) EphemeralBytes() (uint64, error) {
	var size block.Size
	if err := size.UnmarshalText([]byte(s.EphemeralSize)); err != nil {
		return 0, err
	}
	if size.IsRelative() || size.IsNegative() {
		return 0, fmt.Errorf("must be an absolute size like 40GiB")
	}
	return size.Value(), nil
}

func (c *Cluster) SharesSystemDisk(n Node) bool {
	return c.Spec.Storage.SystemDisk && len(n.DataDisks) == 0
}

type ClusterAuth struct {
	OIDC *ClusterOIDC `yaml:"oidc,omitempty" json:"oidc,omitempty"`
}

// ClusterOIDC becomes a JWT authenticator in the API server's AuthenticationConfiguration.
// Prefixes default to "oidc:" so SSO users and groups never collide with service accounts.
type ClusterOIDC struct {
	Issuer         string `yaml:"issuer" json:"issuer"`
	ClientID       string `yaml:"clientID" json:"clientID"`
	UsernameClaim  string `yaml:"usernameClaim,omitempty" json:"usernameClaim,omitempty"`
	UsernamePrefix string `yaml:"usernamePrefix,omitempty" json:"usernamePrefix,omitempty"`
	GroupsClaim    string `yaml:"groupsClaim,omitempty" json:"groupsClaim,omitempty"`
	GroupsPrefix   string `yaml:"groupsPrefix,omitempty" json:"groupsPrefix,omitempty"`
	// AdminGroup, when set, is bound to cluster-admin by the platform layer.
	AdminGroup string `yaml:"adminGroup,omitempty" json:"adminGroup,omitempty"`
}

// AdminGroupSubject is the RBAC group name the API server will see for AdminGroup:
// the group with its prefix applied.
func (a ClusterAuth) AdminGroupSubject() string {
	o := a.OIDC
	if o == nil || o.AdminGroup == "" || o.GroupsClaim == "" {
		return ""
	}
	gp := o.GroupsPrefix
	if gp == "" {
		gp = "oidc:"
	}
	return gp + o.AdminGroup
}

// AuthenticationConfig renders the API server's structured AuthenticationConfiguration
// (one JWT authenticator); nil when unset.
func (a ClusterAuth) AuthenticationConfig() map[string]any {
	o := a.OIDC
	if o == nil || o.Issuer == "" || o.ClientID == "" {
		return nil
	}
	username := map[string]any{"claim": "sub"}
	if o.UsernameClaim != "" {
		username["claim"] = o.UsernameClaim
	}
	prefix := o.UsernamePrefix
	if prefix == "" {
		prefix = "oidc:"
	}
	username["prefix"] = prefix
	mappings := map[string]any{"username": username}
	if o.GroupsClaim != "" {
		gp := o.GroupsPrefix
		if gp == "" {
			gp = "oidc:"
		}
		mappings["groups"] = map[string]any{"claim": o.GroupsClaim, "prefix": gp}
	}
	return map[string]any{
		"apiVersion": "apiserver.config.k8s.io/v1",
		"kind":       "AuthenticationConfiguration",
		"jwt": []any{map[string]any{
			"issuer":        map[string]any{"url": o.Issuer, "audiences": []any{o.ClientID}},
			"claimMappings": mappings,
		}},
	}
}

// Backup declares what Kubit keeps on the admin host for disaster recovery.
type Backup struct {
	Etcd EtcdBackup `yaml:"etcd" json:"etcd"`
}

// EtcdBackup schedules etcd snapshots over the Talos API. Interval "0" disables the
// schedule; snapshots can still be taken by hand.
type EtcdBackup struct {
	Interval string `yaml:"interval,omitempty" json:"interval,omitempty"` // Go duration, default 6h
	Keep     int    `yaml:"keep,omitempty" json:"keep,omitempty"`         // scheduled snapshots retained, default 28
}

// IntervalDuration parses Interval; zero means disabled.
func (b EtcdBackup) IntervalDuration() time.Duration {
	d, err := time.ParseDuration(b.Interval)
	if err != nil || d <= 0 {
		return 0
	}
	return d
}

// Pool is a node class in the sense of Omni machine classes or CAPI machine pools.
type Pool struct {
	Name        string            `yaml:"name" json:"name"`
	Role        Role              `yaml:"role" json:"role"`
	Labels      map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
	Taints      map[string]string `yaml:"taints,omitempty" json:"taints,omitempty"` // key: "value:Effect"
	Annotations map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty"`
	// Extensions, when set, replace the cluster default and give the pool its own schematic.
	Extensions  []string     `yaml:"extensions,omitempty" json:"extensions,omitempty"`
	SchematicID string       `yaml:"schematicID,omitempty" json:"schematicID,omitempty"`
	InstallDisk *InstallDisk `yaml:"installDisk,omitempty" json:"installDisk,omitempty"`
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
	// Nameservers and NTP apply to every node (Talos defaults otherwise).
	Nameservers []string `yaml:"nameservers,omitempty" json:"nameservers,omitempty"`
	NTP         []string `yaml:"ntp,omitempty" json:"ntp,omitempty"`
}

type Node struct {
	Hostname string `yaml:"hostname" json:"hostname"`
	// IP is where the machine is reachable now (its lease, or the static address).
	IP string `yaml:"ip" json:"ip"`
	// MAC of the uplink is the machine's identity; UUID (SMBIOS) is a second one.
	MAC  string `yaml:"mac,omitempty" json:"mac,omitempty"`
	UUID string `yaml:"uuid,omitempty" json:"uuid,omitempty"`
	// Pool names the node class; Role is derived from it (kept for legacy declarations).
	Pool        string      `yaml:"pool,omitempty" json:"pool,omitempty"`
	Role        Role        `yaml:"role,omitempty" json:"role,omitempty"`
	Arch        Arch        `yaml:"arch,omitempty" json:"arch,omitempty"`
	InstallDisk InstallDisk `yaml:"installDisk,omitempty" json:"installDisk,omitempty"`
	// DataDisks are whole disks Talos formats (xfs) and mounts at /var/mnt/data-N, in
	// this order, for node-local storage; nothing else on the machine is touched.
	DataDisks []string `yaml:"dataDisks,omitempty" json:"dataDisks,omitempty"`
	// KVM marks nodes where /dev/kvm exists, enabling the runsc-kvm RuntimeClass.
	KVM bool `yaml:"kvm,omitempty" json:"kvm,omitempty"`
	// Network, when set, replaces DHCP on the uplink with static addressing.
	Network     *NodeNetwork      `yaml:"network,omitempty" json:"network,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
	Taints      map[string]string `yaml:"taints,omitempty" json:"taints,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty"`
}

// TargetIP is where the node answers once its config is applied: the first static
// address when one is declared, otherwise the address it was discovered on.
func (n Node) TargetIP() string {
	if n.Network != nil && len(n.Network.Addresses) > 0 {
		if pfx, err := netip.ParsePrefix(n.Network.Addresses[0]); err == nil {
			return pfx.Addr().String()
		}
		if a, err := netip.ParseAddr(n.Network.Addresses[0]); err == nil {
			return a.String()
		}
	}
	return n.IP
}

type NodeNetwork struct {
	Addresses   []string `yaml:"addresses" json:"addresses"` // CIDR notation
	Gateway     string   `yaml:"gateway,omitempty" json:"gateway,omitempty"`
	Nameservers []string `yaml:"nameservers,omitempty" json:"nameservers,omitempty"`
	VLAN        uint16   `yaml:"vlan,omitempty" json:"vlan,omitempty"`
	MTU         uint32   `yaml:"mtu,omitempty" json:"mtu,omitempty"`
}

// MaxDataDisks bounds the data volumes per node; the mount names are data-1..data-N.
const MaxDataDisks = 8

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
	Flux          Flux    `yaml:"flux" json:"flux"`
	// Longhorn is replicated block storage on the nodes' data disks: the default
	// StorageClass, volume snapshots, backups to S3. Needs dataDisks on the nodes
	// that should hold replicas; Talos gets the iscsi and util-linux extensions.
	Longhorn Addon `yaml:"longhorn" json:"longhorn"`
	Builds   Addon `yaml:"builds" json:"builds"`
}

// AddOns reports whether any in-cluster add-on is enabled: the workers then carry
// MetalLB, ingress and metrics pods on top of the kubelet.
func (p Platform) AddOns() bool {
	return p.MetalLB.Enabled || p.IngressNginx.Enabled || p.MetricsServer.Enabled || p.CertManager.Enabled || p.Flux.Enabled || p.Longhorn.Enabled || p.Builds.Enabled
}

const (
	RegistryHost = "registry.kubit"
	RegistryPort = 5000
)

func (c *Cluster) RegistryIP() string {
	_, end, err := ParseIPRange(c.Spec.Platform.MetalLB.Range)
	if err != nil {
		return ""
	}
	return end.String()
}

func (c *Cluster) MetalLBPool() string {
	r := c.Spec.Platform.MetalLB.Range
	if !c.Spec.Platform.Builds.Enabled {
		return r
	}
	start, end, err := ParseIPRange(r)
	if err != nil || start == end {
		return r
	}
	return start.String() + "-" + end.Prev().String()
}

// LonghornExtensions are the Talos system extensions Longhorn's engine needs.
var LonghornExtensions = []string{"siderolabs/iscsi-tools", "siderolabs/util-linux-tools"}

// LonghornNodes are the nodes that carry Longhorn replicas: those with data disks,
// and those sharing their system disk.
func (c *Cluster) LonghornNodes() []Node {
	var out []Node
	for _, n := range c.Spec.Nodes {
		if len(n.DataDisks) > 0 || c.SharesSystemDisk(n) {
			out = append(out, n)
		}
	}
	return out
}

// LonghornReplicas is the default replica count: three, or fewer on small clusters.
func (c *Cluster) LonghornReplicas() int {
	n := len(c.LonghornNodes())
	if n > 3 {
		return 3
	}
	if n < 1 {
		return 1
	}
	return n
}

type Addon struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
	// Values are merged into the add-on's Helm chart values (free-form).
	Values map[string]any `yaml:"values,omitempty" json:"values,omitempty"`
}

type Flux struct {
	Enabled    bool            `yaml:"enabled" json:"enabled"`
	Repository *FluxRepository `yaml:"repository,omitempty" json:"repository,omitempty"`
	Values     map[string]any  `yaml:"values,omitempty" json:"values,omitempty"`
}

type FluxRepository struct {
	URL      string `yaml:"url" json:"url"`
	Branch   string `yaml:"branch,omitempty" json:"branch,omitempty"`
	Path     string `yaml:"path,omitempty" json:"path,omitempty"`
	Interval string `yaml:"interval,omitempty" json:"interval,omitempty"`
}

func (r *FluxRepository) Default() {
	if r.Branch == "" {
		r.Branch = "main"
	}
	if r.Path == "" {
		r.Path = "./"
	}
	if r.Interval == "" {
		r.Interval = "5m"
	}
}

func (r *FluxRepository) Validate() error {
	var errs []error
	if u, err := url.Parse(r.URL); err != nil || u.Scheme != "https" || u.Host == "" {
		errs = append(errs, fmt.Errorf("platform.flux.repository.url must be an https:// URL"))
	}
	if strings.HasPrefix(r.Path, "/") || slices.Contains(strings.Split(r.Path, "/"), "..") {
		errs = append(errs, fmt.Errorf("platform.flux.repository.path %q must be relative to the repository root", r.Path))
	}
	if strings.ContainsAny(r.Branch, " \t\n") {
		errs = append(errs, fmt.Errorf("platform.flux.repository.branch %q is not a branch name", r.Branch))
	}
	if d, err := time.ParseDuration(r.Interval); err != nil || d < 10*time.Second {
		errs = append(errs, fmt.Errorf("platform.flux.repository.interval %q: a Go duration of at least 10s", r.Interval))
	}
	return errors.Join(errs...)
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
	if c.Spec.Backup.Etcd.Interval == "" {
		c.Spec.Backup.Etcd.Interval = "6h"
	}
	if c.Spec.Backup.Etcd.Keep == 0 {
		c.Spec.Backup.Etcd.Keep = 28
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
	if c.Spec.Storage.SystemDisk && c.Spec.Storage.EphemeralSize == "" {
		c.Spec.Storage.EphemeralSize = DefaultEphemeralSize
	}
	if r := c.Spec.Platform.Flux.Repository; r != nil && r.URL == "" {
		c.Spec.Platform.Flux.Repository = nil
	}
	if r := c.Spec.Platform.Flux.Repository; r != nil {
		r.Default()
	}
	if c.Spec.Platform.GVisor.Enabled && !containsString(c.Spec.Extensions, "siderolabs/gvisor") {
		c.Spec.Extensions = append(c.Spec.Extensions, "siderolabs/gvisor")
	}
	if c.Spec.Platform.Longhorn.Enabled {
		for _, e := range LonghornExtensions {
			if !containsString(c.Spec.Extensions, e) {
				c.Spec.Extensions = append(c.Spec.Extensions, e)
			}
		}
	}
	if c.Spec.ControlPlane.AllowScheduling == nil {
		v := len(c.Spec.Nodes) < 6
		c.Spec.ControlPlane.AllowScheduling = &v
	}
	c.defaultPools()
	for i := range c.Spec.Nodes {
		n := &c.Spec.Nodes[i]
		if n.Arch == "" {
			n.Arch = ArchAMD64
		}
		if n.Pool == "" {
			// Legacy declaration: role names the default pool.
			if n.Role == "" {
				n.Role = RoleWorker
			}
			n.Pool = string(n.Role)
		}
		if p := c.poolByName(n.Pool); p != nil {
			n.Role = p.Role
			if n.InstallDisk.Path == "" && n.InstallDisk.Selector == nil && p.InstallDisk != nil {
				n.InstallDisk = *p.InstallDisk
			}
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

// defaultPools guarantees a "controlplane" and a "worker" pool exist so declarations
// that only use role: keep working and every node has a pool.
func (c *Cluster) defaultPools() {
	have := map[string]bool{}
	for _, p := range c.Spec.Pools {
		have[p.Name] = true
	}
	if !have["controlplane"] {
		c.Spec.Pools = append([]Pool{{Name: "controlplane", Role: RoleControlPlane}}, c.Spec.Pools...)
	}
	if !have["worker"] {
		c.Spec.Pools = append(c.Spec.Pools, Pool{Name: "worker", Role: RoleWorker})
	}
	for i := range c.Spec.Pools {
		if c.Spec.Pools[i].Role == "" {
			c.Spec.Pools[i].Role = RoleWorker
		}
	}
}

func (c *Cluster) poolByName(name string) *Pool {
	for i := range c.Spec.Pools {
		if c.Spec.Pools[i].Name == name {
			return &c.Spec.Pools[i]
		}
	}
	return nil
}

// PoolOf returns the node's pool (always resolvable after Parse).
func (c *Cluster) PoolOf(n Node) Pool {
	if p := c.poolByName(n.Pool); p != nil {
		return *p
	}
	return Pool{Name: n.Pool, Role: n.Role}
}

// ExtensionsFor is the extension set a pool installs: its own, else the cluster default.
func (c *Cluster) ExtensionsFor(p Pool) []string {
	if len(p.Extensions) > 0 {
		return p.Extensions
	}
	return c.Spec.Extensions
}

// SchematicFor is the schematic a pool installs from; pools without their own
// extensions share the cluster schematic.
func (c *Cluster) SchematicFor(p Pool) string {
	if len(p.Extensions) > 0 && p.SchematicID != "" {
		return p.SchematicID
	}
	if len(p.Extensions) > 0 {
		return ""
	}
	return c.Spec.SchematicID
}

// NodeLabels merges pool labels under node labels.
func (c *Cluster) NodeLabels(n Node) map[string]string {
	return merge(c.PoolOf(n).Labels, n.Labels)
}

func (c *Cluster) NodeTaints(n Node) map[string]string { return merge(c.PoolOf(n).Taints, n.Taints) }

func (c *Cluster) NodeAnnotations(n Node) map[string]string {
	return merge(c.PoolOf(n).Annotations, n.Annotations)
}

func merge(base, over map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		out[k] = v
	}
	return out
}

var hostnameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func (c *Cluster) Validate() error {
	var errs []error
	if c.APIVersion != APIVersion || c.Kind != KindCluster {
		errs = append(errs, fmt.Errorf("expected apiVersion %s kind %s", APIVersion, KindCluster))
	}
	if iv := c.Spec.Backup.Etcd.Interval; iv != "" && iv != "0" {
		if d, err := time.ParseDuration(iv); err != nil || d < 5*time.Minute {
			errs = append(errs, fmt.Errorf("backup.etcd.interval %q: a Go duration of at least 5m, or 0 to disable", iv))
		}
	}
	if c.Spec.Backup.Etcd.Keep < 1 {
		errs = append(errs, fmt.Errorf("backup.etcd.keep must be at least 1"))
	}
	if err := c.Spec.Maintenance.Validate(); err != nil {
		errs = append(errs, err)
	}
	if c.Spec.Platform.Longhorn.Enabled && len(c.LonghornNodes()) == 0 {
		errs = append(errs, fmt.Errorf("platform.longhorn needs storage.systemDisk or dataDisks on at least one node to hold replicas"))
	}
	if b := c.Spec.Platform; b.Builds.Enabled {
		start, end, err := ParseIPRange(b.MetalLB.Range)
		if !b.MetalLB.Enabled || err != nil || start == end {
			errs = append(errs, fmt.Errorf("platform.builds needs MetalLB with a range of at least 2 addresses; the last one serves the registry"))
		}
		if !b.Longhorn.Enabled {
			errs = append(errs, fmt.Errorf("platform.builds needs Longhorn for the registry's volume"))
		}
	}
	if s := c.Spec.Storage; s.EphemeralSize != "" {
		if b, err := s.EphemeralBytes(); err != nil {
			errs = append(errs, fmt.Errorf("storage.ephemeralSize %q: %w", s.EphemeralSize, err))
		} else if b < MinEphemeralBytes {
			errs = append(errs, fmt.Errorf("storage.ephemeralSize %q: at least 10GiB", s.EphemeralSize))
		}
	}
	if r := c.Spec.Platform.Flux.Repository; r != nil {
		if err := r.Validate(); err != nil {
			errs = append(errs, err)
		}
	}
	if o := c.Spec.Auth.OIDC; o != nil {
		if !strings.HasPrefix(o.Issuer, "https://") {
			errs = append(errs, fmt.Errorf("auth.oidc.issuer must be an https:// URL"))
		}
		if o.ClientID == "" {
			errs = append(errs, fmt.Errorf("auth.oidc.clientID is required"))
		}
		if o.AdminGroup != "" && o.GroupsClaim == "" {
			errs = append(errs, fmt.Errorf("auth.oidc.adminGroup needs groupsClaim"))
		}
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
	poolNames := map[string]bool{}
	cpPools := 0
	for i, pl := range c.Spec.Pools {
		pp := fmt.Sprintf("pools[%d]", i)
		if !hostnameRE.MatchString(pl.Name) {
			errs = append(errs, fmt.Errorf("%s.name %q must be a DNS label", pp, pl.Name))
		}
		if poolNames[pl.Name] {
			errs = append(errs, fmt.Errorf("%s.name %q duplicated", pp, pl.Name))
		}
		poolNames[pl.Name] = true
		if pl.Role != RoleControlPlane && pl.Role != RoleWorker {
			errs = append(errs, fmt.Errorf("%s.role %q must be controlplane or worker", pp, pl.Role))
		}
		if pl.Role == RoleControlPlane {
			cpPools++
		}
		for k, v := range pl.Taints {
			if err := validTaint(k, v); err != nil {
				errs = append(errs, fmt.Errorf("%s.taints: %w", pp, err))
			}
		}
	}
	if cpPools != 1 {
		errs = append(errs, fmt.Errorf("exactly one pool must have role controlplane, found %d", cpPools))
	}
	var staticAddrs []netip.Prefix
	seenHost, seenIP, seenMAC := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for i, n := range c.Spec.Nodes {
		p := fmt.Sprintf("nodes[%d]", i)
		if !poolNames[n.Pool] {
			errs = append(errs, fmt.Errorf("%s.pool %q is not declared in spec.pools", p, n.Pool))
		}
		if n.MAC != "" {
			if seenMAC[strings.ToLower(n.MAC)] {
				errs = append(errs, fmt.Errorf("%s.mac %q duplicated", p, n.MAC))
			}
			seenMAC[strings.ToLower(n.MAC)] = true
		}
		for k, v := range n.Taints {
			if err := validTaint(k, v); err != nil {
				errs = append(errs, fmt.Errorf("%s.taints: %w", p, err))
			}
		}
		if nn := n.Network; nn != nil {
			if len(nn.Addresses) == 0 {
				errs = append(errs, fmt.Errorf("%s.network.addresses must not be empty", p))
			}
			for _, a := range nn.Addresses {
				pfx, err := netip.ParsePrefix(a)
				if err != nil {
					errs = append(errs, fmt.Errorf("%s.network.addresses %q: must be CIDR notation", p, a))
					continue
				}
				for _, other := range staticAddrs {
					if other.Addr() == pfx.Addr() {
						errs = append(errs, fmt.Errorf("%s.network.addresses %q used by another node", p, a))
					}
				}
				staticAddrs = append(staticAddrs, pfx)
				if v := c.Spec.ControlPlane.VIP; v != "" && v == pfx.Addr().String() {
					errs = append(errs, fmt.Errorf("%s.network.addresses %q collides with the control plane VIP", p, a))
				}
				if m := c.Spec.Platform.MetalLB; m.Enabled {
					if lo, hi, err := ParseIPRange(m.Range); err == nil && !pfx.Addr().Less(lo) && !hi.Less(pfx.Addr()) {
						errs = append(errs, fmt.Errorf("%s.network.addresses %q lies inside the MetalLB range", p, a))
					}
				}
			}
			if nn.Gateway != "" {
				if _, err := netip.ParseAddr(nn.Gateway); err != nil {
					errs = append(errs, fmt.Errorf("%s.network.gateway: %w", p, err))
				}
			}
			for _, ns := range nn.Nameservers {
				if _, err := netip.ParseAddr(ns); err != nil {
					errs = append(errs, fmt.Errorf("%s.network.nameservers %q: %w", p, ns, err))
				}
			}
			if len(nn.Nameservers) == 0 && len(c.Spec.Network.Nameservers) == 0 {
				errs = append(errs, fmt.Errorf("%s uses static addressing but no nameservers are set (on the node or the cluster) — the node would have no DNS and image pulls would fail; add network.nameservers", p))
			}
			if nn.VLAN > 4094 {
				errs = append(errs, fmt.Errorf("%s.network.vlan %d out of range", p, nn.VLAN))
			}
		}
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
		if n.Arch != ArchAMD64 && n.Arch != ArchARM64 {
			errs = append(errs, fmt.Errorf("%s.arch %q must be amd64 or arm64", p, n.Arch))
		}
		if (n.InstallDisk.Path == "") == (n.InstallDisk.Selector == nil) {
			errs = append(errs, fmt.Errorf("%s.installDisk needs exactly one of path or selector", p))
		}
		if len(n.DataDisks) > MaxDataDisks {
			errs = append(errs, fmt.Errorf("%s.dataDisks: at most %d", p, MaxDataDisks))
		}
		seenDisk := map[string]bool{}
		for _, d := range n.DataDisks {
			switch {
			case d == "":
				errs = append(errs, fmt.Errorf("%s.dataDisks: empty path", p))
			case d == n.InstallDisk.Path:
				errs = append(errs, fmt.Errorf("%s.dataDisks: %s is the install disk", p, d))
			case seenDisk[d]:
				errs = append(errs, fmt.Errorf("%s.dataDisks: %s listed twice", p, d))
			}
			seenDisk[d] = true
		}
	}
	if m := c.Spec.Platform.MetalLB; m.Enabled {
		if _, _, err := ParseIPRange(m.Range); err != nil {
			errs = append(errs, fmt.Errorf("platform.metallb.range: %w", err))
		}
	}
	for _, ns := range c.Spec.Network.Nameservers {
		if _, err := netip.ParseAddr(ns); err != nil {
			errs = append(errs, fmt.Errorf("network.nameservers %q: %w", ns, err))
		}
	}
	return errors.Join(errs...)
}

// validTaint accepts Kubernetes taints written as key: "value:Effect" or key: "Effect".
func validTaint(key, value string) error {
	effect := value
	if i := strings.LastIndexByte(value, ':'); i >= 0 {
		effect = value[i+1:]
	}
	switch effect {
	case "NoSchedule", "PreferNoSchedule", "NoExecute":
		return nil
	}
	return fmt.Errorf("%s=%q: effect must be NoSchedule, PreferNoSchedule or NoExecute (write value:Effect)", key, value)
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
