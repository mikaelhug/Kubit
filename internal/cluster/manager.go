package cluster

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/factory"
	"github.com/mikael/kubit/internal/k8s"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	StateProvisioning = "provisioning"
	StateBootstrapped = "bootstrapped"
	StateReady        = "ready"
	StateFailed       = "failed"
)

func Observable(state string) bool {
	return state == StateReady || state == StateBootstrapped
}

const (
	NodeDiscovered = "discovered"
	NodeInstalling = "installing"
	NodeJoined     = "joined"
	NodeReady      = "ready"
	NodeFailed     = "failed"
)

type Timeouts struct {
	Install   time.Duration
	Bootstrap time.Duration
	Ready     time.Duration
}

var defaultTimeouts = Timeouts{Install: 10 * time.Minute, Bootstrap: 5 * time.Minute, Ready: 10 * time.Minute}

type Manager struct {
	Store    *store.Store
	Factory  *factory.Client
	Timeouts Timeouts
	Home     string

	kubeMu sync.Mutex
	kube   map[string]kubeEntry

	talosMu   sync.Mutex
	talos     map[string]map[string]talosEntry
	talosGone map[string]bool

	addonErrMu sync.Mutex
	addonErr   map[string]string

	statesMu sync.Mutex
	states   map[string]PlatformState
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
	m := &Manager{Store: s, Factory: factory.New(), Timeouts: defaultTimeouts, Home: home}
	s.OnChange(func(c store.Change) {
		if c.Table != "clusters" {
			return
		}
		switch c.Op {
		case "delete":
			m.dropKube(c.Cluster)
			m.forgetTalos(c.Cluster)
		case "put":
			m.rememberTalos(c.Cluster)
		}
	})
	return m
}

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
	return ImageStatus{TalosVersion: c.Spec.TalosVersion, Installed: c.Spec.SchematicID, Desired: id, Extensions: append([]string{}, c.Spec.Extensions...), Outdated: imageOutdated(c, id, pools)}, nil
}

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
	if old, _, err := m.LoadCluster(ctx, c.Metadata.Name); err == nil {
		installed, split := m.installedLayout(ctx, old)
		if err := config.CheckChange(old, c, installed, split); err != nil {
			return invalidSpec{err}
		}
	}
	spec, err := c.Marshal()
	if err != nil {
		return err
	}
	return m.Store.PutCluster(ctx, store.ClusterRow{Name: c.Metadata.Name, Spec: spec, SchematicID: c.Spec.SchematicID, State: state})
}

type invalidSpec struct{ error }

func (e invalidSpec) Unwrap() error { return e.error }

func (e invalidSpec) Is(target error) bool { return target == store.ErrInvalid }

func (m *Manager) saveExisting(ctx context.Context, c *config.Cluster) error {
	if _, err := m.Store.GetCluster(ctx, c.Metadata.Name); err != nil {
		return err
	}
	return m.SaveCluster(ctx, c, "")
}

func (m *Manager) KubeClient(ctx context.Context, name string) (*k8s.Client, error) {
	_, kc, err := m.clusterClients(ctx, name)
	return kc, err
}

func (m *Manager) KubeClientFor(name string, sec *store.ClusterSecrets) (*k8s.Client, error) {
	if sec.Kubeconfig == nil {
		return nil, fmt.Errorf("cluster %s has no kubeconfig yet", name)
	}
	sum := sha256.Sum256(sec.Kubeconfig)
	m.kubeMu.Lock()
	defer m.kubeMu.Unlock()
	if e, ok := m.kube[name]; ok && e.sum == sum {
		return e.kc, nil
	}
	kc, err := k8s.New(sec.Kubeconfig)
	if err != nil {
		return nil, err
	}
	if m.kube == nil {
		m.kube = map[string]kubeEntry{}
	}
	m.kube[name] = kubeEntry{sum: sum, kc: kc}
	return kc, nil
}

func (m *Manager) dropKube(name string) {
	m.kubeMu.Lock()
	defer m.kubeMu.Unlock()
	delete(m.kube, name)
}

const talosRetireDelay = time.Minute

func (m *Manager) talosClientFor(name, ip string, talosconfig []byte) (*talos.Client, error) {
	sum := sha256.Sum256(talosconfig)
	m.talosMu.Lock()
	defer m.talosMu.Unlock()
	if m.talosGone[name] {
		return nil, fmt.Errorf("cluster %q: %w", name, store.ErrNotFound)
	}
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

func (m *Manager) dropTalosCluster(name string) {
	m.talosMu.Lock()
	defer m.talosMu.Unlock()
	m.dropTalosLocked(name)
}

func (m *Manager) forgetTalos(name string) {
	m.talosMu.Lock()
	defer m.talosMu.Unlock()
	m.dropTalosLocked(name)
	if m.talosGone == nil {
		m.talosGone = map[string]bool{}
	}
	m.talosGone[name] = true
}

func (m *Manager) rememberTalos(name string) {
	m.talosMu.Lock()
	defer m.talosMu.Unlock()
	delete(m.talosGone, name)
}

func (m *Manager) dropTalosLocked(name string) {
	for _, e := range m.talos[name] {
		retire(e.tc)
	}
	delete(m.talos, name)
}

func retire(tc *talos.Client) {
	time.AfterFunc(talosRetireDelay, func() { tc.Close() })
}

func (m *Manager) clusterClients(ctx context.Context, name string) (*store.ClusterSecrets, *k8s.Client, error) {
	sec, err := m.Store.GetClusterSecrets(ctx, name)
	if err != nil {
		m.dropClientsIfGone(name, err)
		return nil, nil, err
	}
	kc, err := m.KubeClientFor(name, sec)
	if err != nil {
		return nil, nil, err
	}
	return sec, kc, nil
}

func (m *Manager) dropClientsIfGone(name string, err error) {
	if errors.Is(err, store.ErrNotFound) {
		m.dropKube(name)
		m.dropTalosCluster(name)
	}
}

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
		return m.Store.PutNodeMachineConfig(ctx, n.IP, cfg, config.HasSystemVolume(cfg))
	}
	return nil
}

func marshalJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func (m *Manager) installedLayout(ctx context.Context, c *config.Cluster) (installed, split map[string]bool) {
	installed, split = map[string]bool{}, map[string]bool{}
	for _, n := range c.Spec.Nodes {
		cfg, systemSplit, err := m.Store.NodeMachineConfigSplit(ctx, n.IP)
		if err != nil {
			continue
		}
		installed[n.IP] = true
		split[n.IP] = systemSplit || config.HasSystemVolume(cfg)
	}
	return installed, split
}
