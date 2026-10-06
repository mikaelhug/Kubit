package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/k8s"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
)

const (
	HealthHealthy  = "healthy"
	HealthDegraded = "degraded"
	HealthDown     = "down"
	HealthUnknown  = "unknown"
)

type Status struct {
	Name              string       `json:"name"`
	State             string       `json:"state"`
	TalosVersion      string       `json:"talosVersion"`
	KubernetesVersion string       `json:"kubernetesVersion"`
	Endpoint          string       `json:"endpoint"`
	APIReachable      bool         `json:"apiReachable"`
	APIError          string       `json:"apiError,omitempty"`
	APIReach          string       `json:"apiReach,omitempty"`
	Nodes             []NodeStatus `json:"nodes"`
	Etcd              EtcdStatus   `json:"etcd"`
	Totals            Totals       `json:"totals"`
	IngressIP         string       `json:"ingressIP,omitempty"`
	Health            string       `json:"health,omitempty"`
	OpenAlerts        int          `json:"openAlerts,omitempty"`
	ObservedAt        string       `json:"observedAt"`
	LastSnapshotAt    string       `json:"lastSnapshotAt,omitempty"`
	Observer          string       `json:"observer,omitempty"`
	ObserverError     string       `json:"observerError,omitempty"`
	LastContactAt     string       `json:"lastContactAt,omitempty"`
}

type NodeStatus struct {
	Hostname       string              `json:"hostname"`
	IP             string              `json:"ip"`
	Role           string              `json:"role"`
	Arch           string              `json:"arch"`
	TalosVersion   string              `json:"talosVersion"`
	KubeletVersion string              `json:"kubeletVersion"`
	Ready          bool                `json:"ready"`
	Unschedulable  bool                `json:"unschedulable"`
	TalosReachable bool                `json:"talosReachable"`
	TalosError     string              `json:"talosError,omitempty"`
	TalosReach     string              `json:"talosReach,omitempty"`
	Registered     bool                `json:"registered"`
	SeenAt         string              `json:"seenAt,omitempty"`
	Stage          string              `json:"stage"`
	CPUMilli       int64               `json:"cpuMilli"`
	CPUCapMilli    int64               `json:"cpuCapMilli"`
	MemBytes       int64               `json:"memBytes"`
	MemCapBytes    int64               `json:"memCapBytes"`
	MemAllocBytes  int64               `json:"memAllocBytes"`
	Pods           int                 `json:"pods"`
	PodCap         int64               `json:"podCap"`
	Temperatures   []talos.Temperature `json:"temperatures,omitempty"`
}

type EtcdStatus struct {
	Members  int      `json:"members"`
	Expected int      `json:"expected"`
	Healthy  bool     `json:"healthy"`
	Leader   string   `json:"leader,omitempty"`
	Alarms   []string `json:"alarms,omitempty"`
}

type Totals struct {
	CPUMilli    int64 `json:"cpuMilli"`
	CPUCapMilli int64 `json:"cpuCapMilli"`
	MemBytes    int64 `json:"memBytes"`
	MemCapBytes int64 `json:"memCapBytes"`
	Pods        int   `json:"pods"`
	PodCap      int64 `json:"podCap"`
	NodesReady  int   `json:"nodesReady"`
	Nodes       int   `json:"nodes"`
}

func (m *Manager) Status(ctx context.Context, name string) (*Status, error) {
	c, row, err := m.LoadCluster(name)
	if err != nil {
		return nil, err
	}
	st := &Status{
		Name: name, State: row.State, TalosVersion: c.Spec.TalosVersion,
		KubernetesVersion: c.Spec.KubernetesVersion, Endpoint: c.Spec.ControlPlane.Endpoint,
	}
	if !Live(row.State) {
		m.nodeRows(ctx, c, st)
		return st, nil
	}
	sec, err := m.Store.GetClusterSecrets(name)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	byHost := m.nodeRows(ctx, c, st)
	var wg sync.WaitGroup
	var mu sync.Mutex
	probeOf := m.probeNodes(ctx, c, sec.Talosconfig, byHost, &wg, &mu)
	if cps := c.ControlPlanes(); len(cps) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e := m.etcdStatus(ctx, name, cps, func(n config.Node) *talos.Client {
				p := probeOf[n.Hostname]
				if p == nil {
					return nil
				}
				select {
				case <-p.dialed:
					return p.tc
				case <-ctx.Done():
					return nil
				}
			})
			mu.Lock()
			st.Etcd = e
			mu.Unlock()
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		m.collectKube(ctx, name, sec.Kubeconfig, st, byHost, &mu)
	}()
	wg.Wait()
	ips := map[string]bool{}
	for _, n := range c.Spec.Nodes {
		ips[n.IP] = true
	}
	m.keepTalos(name, ips)
	st.Observer, st.ObserverError = observerOf(ctx, st)

	st.Etcd.Expected = len(c.ControlPlanes())
	for _, ns := range st.Nodes {
		st.Totals.Nodes++
		if ns.Ready {
			st.Totals.NodesReady++
		}
		st.Totals.CPUMilli += ns.CPUMilli
		st.Totals.CPUCapMilli += ns.CPUCapMilli
		st.Totals.MemBytes += ns.MemBytes
		st.Totals.MemCapBytes += ns.MemCapBytes
		st.Totals.Pods += ns.Pods
		st.Totals.PodCap += ns.PodCap
	}
	st.ObservedAt = time.Now().UTC().Format(time.RFC3339)
	if list, err := m.ListSnapshots(name); err == nil && len(list) > 0 {
		st.LastSnapshotAt = list[0].TS
	}
	return st, nil
}

func (m *Manager) nodeRows(ctx context.Context, c *config.Cluster, st *Status) map[string]*NodeStatus {
	ordered := orderedNodes(c)
	st.Nodes = make([]NodeStatus, len(ordered))
	byHost := map[string]*NodeStatus{}
	for i, n := range ordered {
		st.Nodes[i] = NodeStatus{Hostname: n.Hostname, IP: n.IP, Role: string(n.Role), Arch: string(n.Arch)}
		byHost[n.Hostname] = &st.Nodes[i]
		if n.MAC == "" {
			continue
		}
		if mc, err := m.Store.GetMachine(n.MAC); err == nil && mc.IP != "" && mc.IP != n.IP {
			st.Nodes[i].SeenAt = mc.IP
		}
	}
	return byHost
}

func (m *Manager) probeNodes(ctx context.Context, c *config.Cluster, talosconfig []byte, byHost map[string]*NodeStatus, wg *sync.WaitGroup, mu *sync.Mutex) map[string]*talosProbe {
	probeOf := map[string]*talosProbe{}
	for _, n := range c.Spec.Nodes {
		p := &talosProbe{m: m, cluster: c.Metadata.Name, dialed: make(chan struct{})}
		probeOf[n.Hostname] = p
		wg.Add(1)
		go func() {
			defer wg.Done()
			nctx, cancel := context.WithTimeout(ctx, 6*time.Second)
			defer cancel()
			ver, stage, temps, err := p.probe(nctx, n.IP, talosconfig)
			mu.Lock()
			defer mu.Unlock()
			ns := byHost[n.Hostname]
			if err != nil {
				ns.TalosError = err.Error()
				if Classify(err) == ReachNoNetwork {
					ns.TalosReach = "no-network"
					ns.TalosError = ShortNet(err)
				}
				return
			}
			ns.TalosReachable = true
			ns.Stage = stage
			ns.TalosVersion = ver
			ns.Temperatures = temps
			m.recordHardware(n, p.tc)
		}()
	}
	return probeOf
}

func (m *Manager) collectKube(ctx context.Context, name string, kubeconfig []byte, st *Status, byHost map[string]*NodeStatus, mu *sync.Mutex) {
	kc, err := m.KubeClientFor(name, kubeconfig)
	if err != nil {
		mu.Lock()
		st.APIError = err.Error()
		mu.Unlock()
		return
	}
	err = kc.Ready(ctx)
	var nodes []k8s.NodeStatus
	if err == nil {
		nodes, err = kc.Nodes(ctx)
	}
	if err != nil {
		mu.Lock()
		st.APIError = err.Error()
		if Classify(err) == ReachNoNetwork {
			st.APIReach = "no-network"
		}
		mu.Unlock()
		return
	}
	usage, _ := kc.NodeUsages(ctx)
	pods, _ := kc.PodCount(ctx)
	ingress := kc.LoadBalancerIP(ctx, "traefik", "traefik")
	mu.Lock()
	defer mu.Unlock()
	st.APIReachable = true
	st.IngressIP = ingress
	for _, kn := range nodes {
		ns, ok := byHost[kn.Name]
		if !ok {
			continue
		}
		ns.Registered = true
		ns.Ready = kn.Ready
		ns.Unschedulable = kn.Unschedulable
		ns.KubeletVersion = kn.KubeletVersion
		ns.CPUCapMilli = kn.CapacityCPU
		ns.MemCapBytes = kn.CapacityMem
		ns.MemAllocBytes = kn.AllocatableMem
		ns.PodCap = kn.CapacityPods
		ns.CPUMilli = usage[kn.Name].CPUMilli
		ns.MemBytes = usage[kn.Name].MemoryBytes
		ns.Pods = pods[kn.Name]
	}
}

func observerOf(ctx context.Context, st *Status) (string, string) {
	answered, noNet, failed := false, 0, 0
	for _, n := range st.Nodes {
		if n.TalosReachable {
			answered = true
		} else {
			failed++
			if n.TalosReach == "no-network" {
				noNet++
			}
		}
	}
	if st.APIReachable {
		answered = true
	} else if st.APIError != "" {
		failed++
		if st.APIReach == "no-network" {
			noNet++
		}
	}
	if answered || failed == 0 || noNet != failed {
		return ObserverOnline, ""
	}
	if ControlProbe(ctx, 2*time.Second) != ReachNoNetwork {
		return ObserverOnline, ""
	}
	reason := st.APIError
	for _, n := range st.Nodes {
		if n.TalosReach == "no-network" {
			reason = n.TalosError
			break
		}
	}
	return ObserverOffline, reason
}

const inspectTimeout = 30 * time.Second

func (m *Manager) recordHardware(n config.Node, tc *talos.Client) {
	if n.MAC == "" || tc == nil {
		return
	}
	if mc, err := m.Store.GetMachine(n.MAC); err != nil || !staleInventory(mc, tc.IP) {
		return
	}
	if _, busy := m.inspecting.LoadOrStore(n.MAC, true); busy {
		return
	}
	go func() {
		defer m.inspecting.Delete(n.MAC)
		ctx, cancel := context.WithTimeout(context.Background(), inspectTimeout)
		defer cancel()
		inv, err := tc.Inspect(ctx)
		if err != nil {
			return
		}
		m.RecordInventory(n.MAC, tc.IP, inv)
	}()
}

func staleInventory(mc *store.Machine, ip string) bool {
	var inv talos.Inventory
	if len(mc.Hardware) <= 2 || json.Unmarshal(mc.Hardware, &inv) != nil {
		return true
	}
	addr, _, _ := strings.Cut(inv.UplinkAddress(), "/")
	return addr != ip
}

func (m *Manager) RecordInventory(mac, ip string, inv *talos.Inventory) {
	hw, err := json.Marshal(inv)
	if err != nil {
		return
	}
	m.Store.UpsertNode(store.Machine{MAC: mac, IP: ip, UUID: inv.UUID, Serial: inv.Serial, Arch: inv.Arch, TalosVersion: inv.TalosVersion, Hardware: hw})
}

type talosProbe struct {
	m       *Manager
	cluster string
	dialed  chan struct{}
	tc      *talos.Client
}

func (p *talosProbe) dial(ctx context.Context, ip string, talosconfig []byte) error {
	defer close(p.dialed)
	if err := talos.PortErr(ctx, ip, 2*time.Second); err != nil {
		if Classify(err) == ReachNoNetwork {
			return err
		}
		return fmt.Errorf("port 50000 closed or host down")
	}
	tc, err := p.m.talosClientFor(p.cluster, ip, talosconfig)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	p.tc = tc
	return nil
}

func (p *talosProbe) probe(ctx context.Context, ip string, talosconfig []byte) (version, stage string, temps []talos.Temperature, err error) {
	if err := p.dial(ctx, ip, talosconfig); err != nil {
		return "", "", nil, err
	}
	tc := p.tc
	version, err = versionTag(ctx, tc)
	if err != nil {
		p.m.noteTalosErr(p.cluster, ip, tc, err)
		return "", "", nil, fmt.Errorf("version: %w", talos.ShortGRPC(err))
	}
	stage, err = tc.Stage(ctx)
	if err != nil {
		p.m.noteTalosErr(p.cluster, ip, tc, err)
		return version, "", nil, fmt.Errorf("machine status: %w", talos.ShortGRPC(err))
	}
	temps, _ = tc.Temperatures(ctx)
	return version, stage, temps, nil
}

func (m *Manager) etcdStatus(ctx context.Context, name string, cps []config.Node, dialed func(config.Node) *talos.Client) EtcdStatus {
	var e EtcdStatus
	for _, cp := range cps {
		tc := dialed(cp)
		if tc == nil {
			continue
		}
		members, err := tc.EtcdMemberCount(ctx)
		if err != nil {
			m.noteTalosErr(name, cp.IP, tc, err)
			continue
		}
		e.Members = members
		if info, err := tc.EtcdMemberInfo(ctx); err == nil {
			if info.Leader {
				e.Leader = cp.Hostname
			}
			e.Alarms = append(e.Alarms, info.Errors...)
		}
		if alarms, err := tc.EtcdAlarms(ctx); err == nil {
			e.Alarms = append(e.Alarms, alarms...)
		}
		healthy, _ := tc.ServiceHealthy(ctx, "etcd")
		e.Healthy = healthy && len(e.Alarms) == 0
		break
	}
	return e
}
