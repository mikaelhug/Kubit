package config

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/constants"
	"github.com/siderolabs/talos/pkg/machinery/gendata"
	"go.yaml.in/yaml/v4"
)

const (
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
	Pools             []Pool       `yaml:"pools,omitempty" json:"pools,omitempty"`
	Nodes             []Node       `yaml:"nodes" json:"nodes"`
	Platform          Platform     `yaml:"platform" json:"platform"`
	Backup            Backup       `yaml:"backup" json:"backup"`
	Maintenance       Maintenance  `yaml:"maintenance,omitempty" json:"maintenance,omitempty"`
	Auth              ClusterAuth  `yaml:"auth,omitempty" json:"auth,omitempty"`
	Storage           Storage      `yaml:"storage,omitempty" json:"storage,omitempty"`
}

type ClusterAuth struct {
	OIDC *ClusterOIDC `yaml:"oidc,omitempty" json:"oidc,omitempty"`
}

type ClusterOIDC struct {
	Issuer         string `yaml:"issuer" json:"issuer"`
	ClientID       string `yaml:"clientID" json:"clientID"`
	UsernameClaim  string `yaml:"usernameClaim,omitempty" json:"usernameClaim,omitempty"`
	UsernamePrefix string `yaml:"usernamePrefix,omitempty" json:"usernamePrefix,omitempty"`
	GroupsClaim    string `yaml:"groupsClaim,omitempty" json:"groupsClaim,omitempty"`
	GroupsPrefix   string `yaml:"groupsPrefix,omitempty" json:"groupsPrefix,omitempty"`
	AdminGroup     string `yaml:"adminGroup,omitempty" json:"adminGroup,omitempty"`
}

func (a ClusterAuth) AdminGroupSubject() string {
	o := a.OIDC
	if o == nil || o.AdminGroup == "" || o.GroupsClaim == "" {
		return ""
	}
	return oidcPrefix(o.GroupsPrefix) + o.AdminGroup
}

func oidcPrefix(p string) string {
	if p == "" {
		return "oidc:"
	}
	return p
}

func (a ClusterAuth) AuthenticationConfig() map[string]any {
	o := a.OIDC
	if o == nil || o.Issuer == "" || o.ClientID == "" {
		return nil
	}
	username := map[string]any{"claim": "sub"}
	if o.UsernameClaim != "" {
		username["claim"] = o.UsernameClaim
	}
	username["prefix"] = oidcPrefix(o.UsernamePrefix)
	mappings := map[string]any{"username": username}
	if o.GroupsClaim != "" {
		mappings["groups"] = map[string]any{"claim": o.GroupsClaim, "prefix": oidcPrefix(o.GroupsPrefix)}
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

type Backup struct {
	Etcd EtcdBackup `yaml:"etcd" json:"etcd"`
}

type EtcdBackup struct {
	Interval string `yaml:"interval,omitempty" json:"interval,omitempty"`
	Keep     int    `yaml:"keep,omitempty" json:"keep,omitempty"`
}

func (b EtcdBackup) IntervalDuration() time.Duration {
	d, err := time.ParseDuration(b.Interval)
	if err != nil || d <= 0 {
		return 0
	}
	return d
}

type Pool struct {
	Name        string            `yaml:"name" json:"name"`
	Role        Role              `yaml:"role" json:"role"`
	Labels      map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
	Taints      map[string]string `yaml:"taints,omitempty" json:"taints,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty"`
	Extensions  []string          `yaml:"extensions,omitempty" json:"extensions,omitempty"`
	SchematicID string            `yaml:"schematicID,omitempty" json:"schematicID,omitempty"`
	InstallDisk *InstallDisk      `yaml:"installDisk,omitempty" json:"installDisk,omitempty"`
}

type ControlPlane struct {
	Endpoint        string `yaml:"endpoint,omitempty" json:"endpoint,omitempty"`
	VIP             string `yaml:"vip,omitempty" json:"vip,omitempty"`
	AllowScheduling *bool  `yaml:"allowScheduling,omitempty" json:"allowScheduling,omitempty"`
}

type Node struct {
	Hostname    string            `yaml:"hostname" json:"hostname"`
	IP          string            `yaml:"ip" json:"ip"`
	MAC         string            `yaml:"mac,omitempty" json:"mac,omitempty"`
	UUID        string            `yaml:"uuid,omitempty" json:"uuid,omitempty"`
	Pool        string            `yaml:"pool,omitempty" json:"pool,omitempty"`
	Role        Role              `yaml:"role,omitempty" json:"role,omitempty"`
	Arch        Arch              `yaml:"arch,omitempty" json:"arch,omitempty"`
	InstallDisk InstallDisk       `yaml:"installDisk,omitempty" json:"installDisk,omitempty"`
	DataDisks   []string          `yaml:"dataDisks,omitempty" json:"dataDisks,omitempty"`
	KVM         bool              `yaml:"kvm,omitempty" json:"kvm,omitempty"`
	Network     *NodeNetwork      `yaml:"network,omitempty" json:"network,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
	Taints      map[string]string `yaml:"taints,omitempty" json:"taints,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty"`
}

type Platform struct {
	MetalLB       MetalLB `yaml:"metallb" json:"metallb"`
	IngressNginx  Addon   `yaml:"ingressNginx" json:"ingressNginx"`
	GVisor        Addon   `yaml:"gvisor" json:"gvisor"`
	MetricsServer Addon   `yaml:"metricsServer" json:"metricsServer"`
	CertManager   Addon   `yaml:"certManager" json:"certManager"`
	Flux          Flux    `yaml:"flux" json:"flux"`
	Longhorn      Addon   `yaml:"longhorn" json:"longhorn"`
	Builds        Addon   `yaml:"builds" json:"builds"`

	LegacyArgoCD *Addon `yaml:"argocd,omitempty" json:"-"`
}

func (p Platform) AddOns() bool {
	return p.MetalLB.Enabled || p.IngressNginx.Enabled || p.MetricsServer.Enabled || p.CertManager.Enabled || p.Flux.Enabled || p.Longhorn.Enabled || p.Builds.Enabled
}

type Addon struct {
	Enabled bool           `yaml:"enabled" json:"enabled"`
	Values  map[string]any `yaml:"values,omitempty" json:"values,omitempty"`
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
	Range   string         `yaml:"range,omitempty" json:"range,omitempty"`
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
	dec := yaml.NewDecoder(bytes.NewReader(b))
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
	if p := &c.Spec.Platform; p.LegacyArgoCD != nil {
		p.Flux.Enabled = p.Flux.Enabled || p.LegacyArgoCD.Enabled
		p.LegacyArgoCD = nil
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
	if c.Spec.Platform.GVisor.Enabled && !slices.Contains(c.Spec.Extensions, "siderolabs/gvisor") {
		c.Spec.Extensions = append(c.Spec.Extensions, "siderolabs/gvisor")
	}
	if c.Spec.Platform.Longhorn.Enabled {
		for _, e := range LonghornExtensions {
			if !slices.Contains(c.Spec.Extensions, e) {
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
			if n.Role == "" {
				n.Role = RoleWorker
			}
			n.Pool = c.defaultPoolFor(n.Role)
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

func (c *Cluster) defaultPools() {
	have := map[string]bool{}
	for i := range c.Spec.Pools {
		if c.Spec.Pools[i].Role == "" {
			c.Spec.Pools[i].Role = RoleWorker
		}
		have[c.Spec.Pools[i].Name] = true
	}
	if c.ControlPlanePool() == nil && !have[string(RoleControlPlane)] {
		c.Spec.Pools = append([]Pool{{Name: string(RoleControlPlane), Role: RoleControlPlane}}, c.Spec.Pools...)
	}
	if !have[string(RoleWorker)] {
		c.Spec.Pools = append(c.Spec.Pools, Pool{Name: string(RoleWorker), Role: RoleWorker})
	}
}

func (c *Cluster) ControlPlanePool() *Pool {
	for i := range c.Spec.Pools {
		if c.Spec.Pools[i].Role == RoleControlPlane {
			return &c.Spec.Pools[i]
		}
	}
	return nil
}

func (c *Cluster) defaultPoolFor(r Role) string {
	if p := c.ControlPlanePool(); r == RoleControlPlane && p != nil {
		return p.Name
	}
	return string(r)
}

func (c *Cluster) poolByName(name string) *Pool {
	for i := range c.Spec.Pools {
		if c.Spec.Pools[i].Name == name {
			return &c.Spec.Pools[i]
		}
	}
	return nil
}

func (c *Cluster) PoolOf(n Node) Pool {
	if p := c.poolByName(n.Pool); p != nil {
		return *p
	}
	return Pool{Name: n.Pool, Role: n.Role}
}

func (c *Cluster) SchematicFor(p Pool) string {
	if len(p.Extensions) > 0 && p.SchematicID != "" {
		return p.SchematicID
	}
	if len(p.Extensions) > 0 {
		return ""
	}
	return c.Spec.SchematicID
}

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

func (c *Cluster) NodeIndex(host string) int {
	for i := range c.Spec.Nodes {
		if c.Spec.Nodes[i].Hostname == host {
			return i
		}
	}
	return -1
}

func (c *Cluster) EndpointNode() (Node, bool) {
	u, err := url.Parse(c.Spec.ControlPlane.Endpoint)
	if err != nil || u.Hostname() == "" || u.Hostname() == c.Spec.ControlPlane.VIP {
		return Node{}, false
	}
	for _, n := range c.ControlPlanes() {
		if n.IP == u.Hostname() {
			return n, true
		}
	}
	return Node{}, false
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
