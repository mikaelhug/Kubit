package cluster

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mikaelhug/kubit/internal/config"
	"github.com/mikaelhug/kubit/internal/factory"
	"github.com/mikaelhug/kubit/internal/k8s"
	"github.com/mikaelhug/kubit/internal/store"
	"github.com/mikaelhug/kubit/internal/talos"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	StateConnecting   = "connecting"
	StateDeclared     = "declared"
	StateBootstrapped = "bootstrapped"
	StateReady        = "ready"
)

func Live(state string) bool { return state == StateBootstrapped || state == StateReady }

const (
	installTimeout   = 10 * time.Minute
	bootstrapTimeout = 5 * time.Minute
	readyTimeout     = 10 * time.Minute
)

type Manager struct {
	Store   *store.Store
	Factory *factory.Client
	Home    string

	kubeMu sync.Mutex
	kube   map[string]kubeEntry

	talosMu sync.Mutex
	talos   map[string]map[string]talosEntry

	addonErrMu sync.Mutex
	addonErr   map[string]string

	desiredMu sync.Mutex
	desired   map[string]*Desired

	inspecting sync.Map

	liveEndpoint map[string]string
	converging   sync.Map
	observed     sync.Map

	Checkout func(url string) string
}

type kubeEntry struct {
	sum [sha256.Size]byte
	kc  *k8s.Client
}

type talosEntry struct {
	sum [sha256.Size]byte
	tc  *talos.Client
}

func NewManager(s *store.Store, home string) *Manager {
	return &Manager{Store: s, Factory: factory.New(), Home: home, desired: map[string]*Desired{}}
}

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

type ImageStatus struct {
	TalosVersion string   `json:"talosVersion"`
	Installed    string   `json:"installed"`
	Desired      string   `json:"desired"`
	Extensions   []string `json:"extensions"`
	Outdated     bool     `json:"outdated"`
}

func (m *Manager) ImageStatus(ctx context.Context, name string) (ImageStatus, error) {
	c, _, err := m.LoadCluster(name)
	if err != nil {
		return ImageStatus{}, err
	}
	id, err := m.Factory.CreateSchematic(ctx, c.Spec.Extensions)
	if err != nil {
		return ImageStatus{}, err
	}
	return ImageStatus{TalosVersion: c.Spec.TalosVersion, Installed: c.Spec.SchematicID, Desired: id, Extensions: append([]string{}, c.Spec.Extensions...), Outdated: id != c.Spec.SchematicID}, nil
}

func (m *Manager) installerImage(c *config.Cluster) string {
	return m.Factory.InstallerImage(c.Spec.SchematicID, c.Spec.TalosVersion)
}

func (m *Manager) machineConfigs(c *config.Cluster, bundle *secrets.Bundle) (*config.Generated, error) {
	return config.Generate(declared(c), bundle, m.installerImage(c))
}

func (m *Manager) loadSecrets(name string) (*Desired, *secrets.Bundle, error) {
	d, err := m.Desired(name)
	if err != nil {
		return nil, nil, err
	}
	return d, d.Bundle, nil
}

func (m *Manager) LoadCluster(name string) (*config.Cluster, *store.ClusterRow, error) {
	row, err := m.Store.GetCluster(name)
	if err != nil {
		return nil, nil, err
	}
	return row.Spec, row, nil
}

func (m *Manager) saveCluster(c *config.Cluster, state string) {
	m.Store.PutCluster(store.ClusterRow{Name: c.Metadata.Name, Spec: c, State: state})
}

func (m *Manager) KubeClient(ctx context.Context, name string) (*k8s.Client, error) {
	_, kc, err := m.clusterClients(ctx, name)
	return kc, err
}

func (m *Manager) KubeClientFor(name string, kubeconfig []byte) (*k8s.Client, error) {
	sum := sha256.Sum256(kubeconfig)
	m.kubeMu.Lock()
	defer m.kubeMu.Unlock()
	if e, ok := m.kube[name]; ok && e.sum == sum {
		return e.kc, nil
	}
	kc, err := k8s.New(kubeconfig)
	if err != nil {
		return nil, err
	}
	if m.kube == nil {
		m.kube = map[string]kubeEntry{}
	}
	m.kube[name] = kubeEntry{sum: sum, kc: kc}
	return kc, nil
}

const talosRetireDelay = time.Minute

func (m *Manager) talosClientFor(name, ip string, talosconfig []byte) (*talos.Client, error) {
	sum := sha256.Sum256(talosconfig)
	m.talosMu.Lock()
	defer m.talosMu.Unlock()
	if e, ok := m.talos[name][ip]; ok {
		if e.sum == sum {
			return e.tc, nil
		}
		retire(e.tc)
		delete(m.talos[name], ip)
	}
	tc, err := talos.Dial(context.Background(), ip, talosconfig)
	if err != nil {
		return nil, err
	}
	if m.talos == nil {
		m.talos = map[string]map[string]talosEntry{}
	}
	if m.talos[name] == nil {
		m.talos[name] = map[string]talosEntry{}
	}
	m.talos[name][ip] = talosEntry{sum: sum, tc: tc}
	return tc, nil
}

func staleConn(tc *talos.Client, err error) bool {
	code := status.Code(err)
	if code == codes.Unavailable {
		return true
	}
	return (code == codes.DeadlineExceeded || errors.Is(err, context.DeadlineExceeded)) && tc.TransientFailure()
}

func (m *Manager) noteTalosErr(name, ip string, tc *talos.Client, err error) {
	if !staleConn(tc, err) {
		return
	}
	m.talosMu.Lock()
	defer m.talosMu.Unlock()
	if e, ok := m.talos[name][ip]; ok && e.tc == tc {
		retire(tc)
		delete(m.talos[name], ip)
	}
}

func (m *Manager) keepTalos(name string, ips map[string]bool) {
	m.talosMu.Lock()
	defer m.talosMu.Unlock()
	for ip, e := range m.talos[name] {
		if !ips[ip] {
			retire(e.tc)
			delete(m.talos[name], ip)
		}
	}
}

func retire(tc *talos.Client) {
	time.AfterFunc(talosRetireDelay, func() { tc.Close() })
}

func (m *Manager) clusterClients(ctx context.Context, name string) (*store.ClusterSecrets, *k8s.Client, error) {
	sec, err := m.Store.GetClusterSecrets(name)
	if err != nil {
		return nil, nil, err
	}
	kc, err := m.KubeClientFor(name, sec.Kubeconfig)
	if err != nil {
		return nil, nil, err
	}
	return sec, kc, nil
}

func (m *Manager) use(d *Desired) {
	name := d.Cluster.Metadata.Name
	m.desiredMu.Lock()
	if prev, ok := m.desired[name]; ok && sameBundle(prev.Bundle, d.Bundle) {
		d.Talosconfig, d.Kubeconfig = prev.Talosconfig, prev.Kubeconfig
	}
	m.desired[name] = d
	m.desiredMu.Unlock()
	m.storeSecrets(d)
}

func storeSecretsOf(talosconfig, kubeconfig []byte) store.ClusterSecrets {
	return store.ClusterSecrets{Talosconfig: talosconfig, Kubeconfig: kubeconfig}
}

func sameBundle(a, b *secrets.Bundle) bool {
	return a != nil && b != nil && a.Cluster != nil && b.Cluster != nil && *a.Cluster == *b.Cluster
}

func (m *Manager) Desired(name string) (*Desired, error) {
	m.desiredMu.Lock()
	defer m.desiredMu.Unlock()
	d, ok := m.desired[name]
	if !ok {
		return nil, fmt.Errorf("cluster %q has no repo: %w", name, store.ErrNotFound)
	}
	return d, nil
}

func (m *Manager) recordNode(c *config.Cluster, n config.Node) {
	m.Store.UpsertNode(store.Machine{IP: n.IP, Cluster: c.Metadata.Name, Hostname: n.Hostname, MAC: n.MAC, UUID: n.UUID, Arch: string(n.Arch), Role: string(n.Role)})
}
