package config

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/netx"
	"github.com/mikael/kubit/internal/yamlx"
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
	TalosVersion      string           `yaml:"talosVersion,omitempty" json:"talosVersion,omitempty"`
	KubernetesVersion string           `yaml:"kubernetesVersion,omitempty" json:"kubernetesVersion,omitempty"`
	Extensions        []string         `yaml:"extensions,omitempty" json:"extensions,omitempty"`
	SchematicID       string           `yaml:"schematicID,omitempty" json:"schematicID,omitempty"`
	ControlPlane      ControlPlane     `yaml:"controlPlane" json:"controlPlane"`
	Network           Network          `yaml:"network" json:"network"`
	Nodes             []Node           `yaml:"nodes" json:"nodes"`
	Platform          Platform         `yaml:"platform" json:"platform"`
	Backup            Backup           `yaml:"backup,omitempty" json:"backup"`
	Auth              ClusterAuth      `yaml:"auth,omitempty" json:"auth,omitempty"`
	Storage           Storage          `yaml:"storage,omitempty" json:"storage,omitempty"`
	Patches           []map[string]any `yaml:"patches,omitempty" json:"patches,omitempty"`
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
	Schedule      string   `yaml:"schedule,omitempty" json:"schedule,omitempty"`
	S3            BackupS3 `yaml:"s3,omitempty" json:"s3,omitempty"`
	AgeRecipients []string `yaml:"ageRecipients,omitempty" json:"ageRecipients,omitempty"`
	Compression   bool     `yaml:"compression,omitempty" json:"compression,omitempty"`
}

type BackupS3 struct {
	Bucket    string `yaml:"bucket,omitempty" json:"bucket,omitempty"`
	Region    string `yaml:"region,omitempty" json:"region,omitempty"`
	Endpoint  string `yaml:"endpoint,omitempty" json:"endpoint,omitempty"`
	Prefix    string `yaml:"prefix,omitempty" json:"prefix,omitempty"`
	PathStyle bool   `yaml:"pathStyle,omitempty" json:"pathStyle,omitempty"`
}

func (b Backup) Enabled() bool {
	return b.Schedule != "" || b.S3.Bucket != "" || len(b.AgeRecipients) > 0
}

const BackupNamespace = "talos-backup"

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
	Role        Role              `yaml:"role,omitempty" json:"role,omitempty"`
	Arch        Arch              `yaml:"arch,omitempty" json:"arch,omitempty"`
	InstallDisk InstallDisk       `yaml:"installDisk,omitempty" json:"installDisk,omitempty"`
	DataDisks   []string          `yaml:"dataDisks,omitempty" json:"dataDisks,omitempty"`
	KVM         bool              `yaml:"kvm,omitempty" json:"kvm,omitempty"`
	Network     *NodeNetwork      `yaml:"network,omitempty" json:"network,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
	Taints      map[string]string `yaml:"taints,omitempty" json:"taints,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty"`
	Patches     []map[string]any  `yaml:"patches,omitempty" json:"patches,omitempty"`
}

type Platform struct {
	MetalLB       MetalLB `yaml:"metallb" json:"metallb"`
	Traefik       Addon   `yaml:"traefik" json:"traefik"`
	GVisor        Toggle  `yaml:"gvisor" json:"gvisor"`
	MetricsServer Addon   `yaml:"metricsServer" json:"metricsServer"`
	CertManager   Addon   `yaml:"certManager" json:"certManager"`
	Flux          Flux    `yaml:"flux" json:"flux"`
	Longhorn      Addon   `yaml:"longhorn" json:"longhorn"`
	Builds        Toggle  `yaml:"builds" json:"builds"`
}

func (p Platform) AddOns() bool {
	return p.MetalLB.Enabled || p.Traefik.Enabled || p.MetricsServer.Enabled || p.CertManager.Enabled || p.Flux.Enabled || p.Longhorn.Enabled || p.Builds.Enabled
}

type Toggle struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
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
		r.Interval = "1m"
	}
}

func (r *FluxRepository) Validate() error {
	var errs []error
	if err := checkRepoURL(r.URL); err != nil {
		errs = append(errs, fmt.Errorf("platform.flux.repository.url: %w", err))
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

var scpRemote = regexp.MustCompile(`^([^@/:]+)@([^/:]+):(.+)$`)

func checkRepoURL(raw string) error {
	if m := scpRemote.FindStringSubmatch(raw); m != nil {
		return fmt.Errorf("write %q as ssh://%s@%s/%s", raw, m[1], m[2], strings.TrimPrefix(m[3], "/"))
	}
	u, err := url.Parse(raw)
	switch {
	case err != nil || u.Host == "":
		return errors.New("an https:// or ssh:// URL with a host")
	case u.Scheme == "https":
		return nil
	case u.Scheme != "ssh":
		return errors.New("an https:// or ssh:// URL")
	case u.User.Username() == "":
		return fmt.Errorf("an ssh:// URL needs a user, as in ssh://git@%s%s", u.Host, u.Path)
	case strings.Trim(u.Path, "/") == "":
		return errors.New("an ssh:// URL needs the repository path")
	}
	return nil
}

type MetalLB struct {
	Enabled bool           `yaml:"enabled" json:"enabled"`
	Range   string         `yaml:"range,omitempty" json:"range,omitempty"`
	Values  map[string]any `yaml:"values,omitempty" json:"values,omitempty"`
}

func Parse(b []byte) (*Cluster, error) {
	var c Cluster
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("cluster.yaml: %w", explainParse(err))
	}
	c.applyDefaults()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

var removedFields = map[string]string{
	"pools":        "set role, labels and taints on each node",
	"pool":         "set role, labels and taints on the node",
	"encryption":   "add a VolumeConfig patch",
	"firewall":     "add NetworkRuleConfig patches",
	"watchdog":     "add a WatchdogTimerConfig patch",
	"tpm":          "delete it",
	"maintenance":  "delete it",
	"etcd":         "use backup.schedule",
	"argocd":       "use platform.flux",
	"ingressNginx": "use platform.traefik",
}

var (
	unknownField = regexp.MustCompile(`field (\S+) not found in type \S+`)
	yamlPrefix   = regexp.MustCompile(`yaml: (construct|unmarshal) errors:\s*`)
)

func explainParse(err error) error {
	msg := unknownField.ReplaceAllStringFunc(err.Error(), func(m string) string {
		f := unknownField.FindStringSubmatch(m)[1]
		if hint, ok := removedFields[f]; ok {
			return f + " is removed; " + hint
		}
		return "unknown field " + f
	})
	return errors.New(strings.TrimSpace(yamlPrefix.ReplaceAllString(msg, "")))
}

func (c *Cluster) Marshal() ([]byte, error) {
	return yamlx.Encode(c)
}

func (c *Cluster) Clone() *Cluster {
	b, err := c.Marshal()
	if err != nil {
		panic(err)
	}
	var out Cluster
	if err := yaml.Unmarshal(b, &out); err != nil {
		panic(err)
	}
	return &out
}

func (c *Cluster) applyDefaults() {
	if c.Spec.TalosVersion == "" {
		c.Spec.TalosVersion = gendata.VersionTag
	}
	if c.Spec.Backup.Enabled() && c.Spec.Backup.S3.Region == "" {
		c.Spec.Backup.S3.Region = "us-east-1"
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
	if c.Spec.Network.Policies == nil {
		c.Spec.Network.Policies = new(true)
	}
	if c.Spec.Network.Discovery == nil {
		c.Spec.Network.Discovery = new(true)
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
	for i := range c.Spec.Nodes {
		n := &c.Spec.Nodes[i]
		if n.Arch == "" {
			n.Arch = ArchAMD64
		}
		if n.Role == "" {
			n.Role = RoleWorker
		}
		if mac := netx.Normalize(n.MAC); mac != "" {
			n.MAC = mac
		}
	}
	if c.Spec.ControlPlane.Endpoint == "" {
		host := c.Spec.ControlPlane.VIP
		if host == "" {
			if cps := c.ControlPlanes(); len(cps) > 0 {
				host = cps[0].TargetIP()
			}
		}
		if host != "" {
			c.Spec.ControlPlane.Endpoint = "https://" + net.JoinHostPort(host, "6443")
		}
	}
}

func (c *Cluster) NodeIndex(host string) int {
	for i := range c.Spec.Nodes {
		if c.Spec.Nodes[i].Hostname == host {
			return i
		}
	}
	return -1
}

func EndpointHost(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func (c *Cluster) EndpointNode() (Node, bool) {
	u, err := url.Parse(c.Spec.ControlPlane.Endpoint)
	if err != nil || u.Hostname() == "" || u.Hostname() == c.Spec.ControlPlane.VIP {
		return Node{}, false
	}
	for _, n := range c.ControlPlanes() {
		if n.IP == u.Hostname() || n.TargetIP() == u.Hostname() {
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
