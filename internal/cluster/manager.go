package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/factory"
	"github.com/mikael/kubit/internal/k8s"
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
	Home string
}

func NewManager(s *store.Store, home string) *Manager {
	return &Manager{Store: s, Factory: factory.New(), Timeouts: DefaultTimeouts, Home: home}
}

// EnsureSchematic resolves spec.schematicID from spec.extensions when unset.
func (m *Manager) EnsureSchematic(ctx context.Context, c *config.Cluster) error {
	if c.Spec.SchematicID != "" {
		return nil
	}
	id, err := m.Factory.CreateSchematic(ctx, c.Spec.Extensions)
	if err != nil {
		return err
	}
	c.Spec.SchematicID = id
	return nil
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

func (m *Manager) recordNode(ctx context.Context, c *config.Cluster, n config.Node, state string, cfg []byte) error {
	if err := m.Store.UpsertNode(ctx, store.NodeRow{IP: n.IP, Cluster: c.Metadata.Name, Hostname: n.Hostname, MAC: n.MAC, Arch: string(n.Arch), Role: string(n.Role), Source: "manual", State: state}); err != nil {
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
