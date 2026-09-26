package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/factory"
	"github.com/mikael/kubit/internal/k8s"
	"github.com/mikael/kubit/internal/labhost"
	"github.com/mikael/kubit/internal/labhost/vfkit"
	"github.com/mikael/kubit/internal/store"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
)

// Cluster lifecycle states persisted in the store.
const (
	StateDeclared     = "declared"
	StateProvisioning = "provisioning"
	StateBootstrapped = "bootstrapped" // Kubernetes API up, platform not yet applied
	StateReady        = "ready"
	StateFailed       = "failed"
)

// Node states persisted in the store.
const (
	NodeDiscovered = "discovered"
	NodeInstalling = "installing"
	NodeJoined     = "joined"
	NodeReady      = "ready"
	NodeFailed     = "failed"
)

type Timeouts struct {
	Install   time.Duration // maintenance apply → mTLS API back
	Bootstrap time.Duration // etcd healthy after bootstrap
	Ready     time.Duration // kubelet registration
}

var DefaultTimeouts = Timeouts{Install: 10 * time.Minute, Bootstrap: 5 * time.Minute, Ready: 10 * time.Minute}

type Manager struct {
	Store    *store.Store
	Factory  *factory.Client
	Timeouts Timeouts
	// Home is $KUBIT_HOME; per-cluster files live under Home/clusters/<name>.
	Home  string
	Local func() (labhost.Driver, error)
}

func NewManager(s *store.Store, home string) *Manager {
	return &Manager{Store: s, Factory: factory.New(), Timeouts: DefaultTimeouts, Home: home, Local: func() (labhost.Driver, error) { return vfkit.New(home) }}
}

// EnsureSchematic resolves the cluster schematic and one per pool that declares its
// own extensions. Schematic IDs are content hashes, so re-running is idempotent.
func (m *Manager) EnsureSchematic(ctx context.Context, c *config.Cluster) error {
	if c.Spec.SchematicID == "" {
		id, err := m.Factory.CreateSchematic(ctx, c.Spec.Extensions)
		if err != nil {
			return err
		}
		c.Spec.SchematicID = id
	}
	for i := range c.Spec.Pools {
		p := &c.Spec.Pools[i]
		if len(p.Extensions) > 0 && p.SchematicID == "" {
			id, err := m.Factory.CreateSchematic(ctx, p.Extensions)
			if err != nil {
				return fmt.Errorf("pool %s: %w", p.Name, err)
			}
			p.SchematicID = id
		}
	}
	return nil
}

func (m *Manager) desiredSchematics(ctx context.Context, c *config.Cluster) (string, map[string]string, error) {
	id, err := m.Factory.CreateSchematic(ctx, c.Spec.Extensions)
	if err != nil {
		return "", nil, err
	}
	pools := map[string]string{}
	for _, p := range c.Spec.Pools {
		if len(p.Extensions) == 0 {
			continue
		}
		pid, err := m.Factory.CreateSchematic(ctx, p.Extensions)
		if err != nil {
			return "", nil, fmt.Errorf("pool %s: %w", p.Name, err)
		}
		pools[p.Name] = pid
	}
	return id, pools, nil
}

func imageOutdated(c *config.Cluster, id string, pools map[string]string) bool {
	if id != c.Spec.SchematicID {
		return true
	}
	for _, p := range c.Spec.Pools {
		if want, ok := pools[p.Name]; ok && want != p.SchematicID {
			return true
		}
	}
	return false
}

type ImageStatus struct {
	TalosVersion string   `json:"talosVersion"`
	Installed    string   `json:"installed"`
	Desired      string   `json:"desired"`
	Extensions   []string `json:"extensions"`
	Outdated     bool     `json:"outdated"`
}

func (m *Manager) ImageStatus(ctx context.Context, name string) (ImageStatus, error) {
	c, _, err := m.LoadCluster(ctx, name)
	if err != nil {
		return ImageStatus{}, err
	}
	id, pools, err := m.desiredSchematics(ctx, c)
	if err != nil {
		return ImageStatus{}, err
	}
	return ImageStatus{TalosVersion: c.Spec.TalosVersion, Installed: c.Spec.SchematicID, Desired: id, Extensions: c.Spec.Extensions, Outdated: imageOutdated(c, id, pools)}, nil
}

// installer maps each pool to its installer image at the cluster's Talos version.
func (m *Manager) installer(c *config.Cluster) config.Installer {
	return func(p config.Pool) string {
		return m.Factory.InstallerImage(c.SchematicFor(p), c.Spec.TalosVersion)
	}
}

func (m *Manager) installerImage(c *config.Cluster) string {
	return m.Factory.InstallerImage(c.Spec.SchematicID, c.Spec.TalosVersion)
}

func (m *Manager) loadSecrets(ctx context.Context, name string) (*store.ClusterSecrets, *secrets.Bundle, error) {
	sec, err := m.Store.GetClusterSecrets(ctx, name)
	if err != nil {
		return nil, nil, err
	}
	bundle, err := config.ParseSecrets(sec.SecretsBundle)
	if err != nil {
		return nil, nil, err
	}
	return sec, bundle, nil
}

// LoadCluster returns the stored declaration.
func (m *Manager) LoadCluster(ctx context.Context, name string) (*config.Cluster, *store.ClusterRow, error) {
	row, err := m.Store.GetCluster(ctx, name)
	if err != nil {
		return nil, nil, err
	}
	c, err := config.Parse(row.Spec)
	if err != nil {
		return nil, nil, fmt.Errorf("stored cluster.yaml: %w", err)
	}
	return c, row, nil
}

func (m *Manager) SaveCluster(ctx context.Context, c *config.Cluster, state string) error {
	if old, row, err := m.LoadCluster(ctx, c.Metadata.Name); err == nil {
		if err := config.CheckChange(old, c, row.State == StateBootstrapped || row.State == StateReady); err != nil {
			return err
		}
	}
	spec, err := c.Marshal()
	if err != nil {
		return err
	}
	return m.Store.PutCluster(ctx, store.ClusterRow{Name: c.Metadata.Name, Spec: spec, SchematicID: c.Spec.SchematicID, State: state})
}

// KubeClient opens the Kubernetes API with the stored admin kubeconfig.
func (m *Manager) KubeClient(ctx context.Context, name string) (*k8s.Client, error) {
	sec, err := m.Store.GetClusterSecrets(ctx, name)
	if err != nil {
		return nil, err
	}
	if sec.Kubeconfig == nil {
		return nil, fmt.Errorf("cluster %s has no kubeconfig yet", name)
	}
	return k8s.New(sec.Kubeconfig)
}

// storeRow is the machine record a declared node maps to.
func storeRow(c *config.Cluster, n config.Node) store.NodeRow {
	return store.NodeRow{IP: n.IP, Cluster: c.Metadata.Name, Hostname: n.Hostname, MAC: n.MAC, UUID: n.UUID, Arch: string(n.Arch), Pool: n.Pool, Role: string(n.Role), Source: "manual"}
}

func (m *Manager) recordNode(ctx context.Context, c *config.Cluster, n config.Node, state string, cfg []byte) error {
	row := storeRow(c, n)
	row.State = state
	if err := m.Store.UpsertNode(ctx, row); err != nil {
		return err
	}
	if cfg != nil {
		return m.Store.PutNodeMachineConfig(ctx, n.IP, cfg)
	}
	return nil
}

func marshalJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
