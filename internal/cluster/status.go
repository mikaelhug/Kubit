package cluster

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
	"github.com/mikael/kubit/internal/tofu"
	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
)

const (
	HealthHealthy  = "healthy"
	HealthDegraded = "degraded"
	HealthDown     = "down"
	HealthUnknown  = "unknown"
)

type Status struct {
	Name              string                `json:"name"`
	State             string                `json:"state"`
	TalosVersion      string                `json:"talosVersion"`
	KubernetesVersion string                `json:"kubernetesVersion"`
	Endpoint          string                `json:"endpoint"`
	APIReachable      bool                  `json:"apiReachable"`
	APIError          string                `json:"apiError,omitempty"`
	APIReach          string                `json:"apiReach,omitempty"`
	Nodes             []NodeStatus          `json:"nodes"`
	Etcd              EtcdStatus            `json:"etcd"`
	Totals            Totals                `json:"totals"`
	Platform          *store.PlatformStatus `json:"platform,omitempty"`
	Health            string                `json:"health,omitempty"`
	OpenAlerts        int                   `json:"openAlerts,omitempty"`
	ObservedAt        string                `json:"observedAt"`
	LastSnapshotAt    string                `json:"lastSnapshotAt,omitempty"`
	SnapshotInterval  string                `json:"snapshotInterval,omitempty"`
	Observer          string                `json:"observer,omitempty"`
	ObserverError     string                `json:"observerError,omitempty"`
	LastContactAt     string                `json:"lastContactAt,omitempty"`
}

type NodeStatus struct {
	Hostname       string `json:"hostname"`
	IP             string `json:"ip"`
	Role           string `json:"role"`
	Arch           string `json:"arch"`
	KVM            bool   `json:"kvm"`
	TalosVersion   string `json:"talosVersion"`
	KubeletVersion string `json:"kubeletVersion"`
	Ready          bool   `json:"ready"`
	Unschedulable  bool   `json:"unschedulable"`
	TalosReachable bool   `json:"talosReachable"`
	TalosError     string `json:"talosError,omitempty"`
	TalosReach     string `json:"talosReach,omitempty"`
	Registered     bool   `json:"registered"`
	Pool           string `json:"pool"`
	SeenAt         string `json:"seenAt,omitempty"`
	Stage          string `json:"stage"`
	CPUMilli       int64  `json:"cpuMilli"`
	CPUCapMilli    int64  `json:"cpuCapMilli"`
	MemBytes       int64  `json:"memBytes"`
	MemCapBytes    int64  `json:"memCapBytes"`
	MemAllocBytes  int64  `json:"memAllocBytes"`
	Pods           int    `json:"pods"`
	PodCap         int64  `json:"podCap"`
	GVisor         bool   `json:"gvisor"`
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
	c, row, err := m.LoadCluster(ctx, name)
	if err != nil {
		return nil, err
	}
	st := &Status{
		Name: name, State: row.State, TalosVersion: c.Spec.TalosVersion,
		KubernetesVersion: c.Spec.KubernetesVersion, Endpoint: c.Spec.ControlPlane.Endpoint,
	}
	if p, err := m.Store.GetPlatformStatus(ctx, name); err == nil && (p.AppliedAt != "" || p.Error != "") {
		for k := range p.Outputs {
			if !tofu.DeclaredOutputs[k] {
				delete(p.Outputs, k)
			}
		}
		st.Platform = p
	}
	sec, err := m.Store.GetClusterSecrets(ctx, name)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	ordered := orderedNodes(c)
	st.Nodes = make([]NodeStatus, len(ordered))
	byHost := map[string]*NodeStatus{}
	var macs []string
	for _, n := range ordered {
		if n.MAC != "" {
			macs = append(macs, n.MAC)
		}
	}
	seen, _ := m.Store.MachineIPs(ctx, macs)
	for i, n := range ordered {
		st.Nodes[i] = NodeStatus{Hostname: n.Hostname, IP: n.IP, Role: string(n.Role), Pool: n.Pool, Arch: string(n.Arch), KVM: n.KVM}
		if ip := seen[strings.ToLower(n.MAC)]; n.MAC != "" && ip != "" && ip != n.IP {
			st.Nodes[i].SeenAt = ip
		}
		byHost[n.Hostname] = &st.Nodes[i]
	}

	probes := make([]*talosProbe, len(c.Spec.Nodes))
	probeOf := map[string]*talosProbe{}
	for i, n := range c.Spec.Nodes {
		probes[i] = &talosProbe{dialed: make(chan struct{})}
		probeOf[n.Hostname] = probes[i]
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i, n := range c.Spec.Nodes {
		wg.Add(1)
		go func(n config.Node, p *talosProbe) {
			defer wg.Done()
			nctx, cancel := context.WithTimeout(ctx, 6*time.Second)
			defer cancel()
			ver, stage, err := p.probe(nctx, n.IP, sec.Talosconfig)
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
		}(n, probes[i])
	}
	if cps := c.ControlPlanes(); len(cps) > 0 && sec.Kubeconfig != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e := m.etcdStatus(ctx, cps, func(n config.Node) *talos.Client {
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
	if sec.Kubeconfig != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			kc, err := m.KubeClientFor(name, sec)
			if err != nil {
				mu.Lock()
				st.APIError = err.Error()
				mu.Unlock()
				return
			}
			nodes, err := kc.Nodes(ctx)
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
			mu.Lock()
			defer mu.Unlock()
			st.APIReachable = true
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
				ns.GVisor = kn.Labels[config.LabelGVisor] == "true"
				ns.CPUMilli = usage[kn.Name].CPUMilli
				ns.MemBytes = usage[kn.Name].MemoryBytes
				ns.Pods = pods[kn.Name]
			}
		}()
	}
	wg.Wait()
	for _, p := range probes {
		if p.tc != nil {
			p.tc.Close()
		}
	}
	st.Observer, st.ObserverError = observe(ctx, st)

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
	st.LastSnapshotAt, _ = m.Store.LatestSnapshotTS(ctx, name)
	st.SnapshotInterval = c.Spec.Backup.Etcd.Interval
	return st, nil
}

func observe(ctx context.Context, st *Status) (string, string) {
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

type talosProbe struct {
	dialed chan struct{}
	tc     *talos.Client
}

func (p *talosProbe) dial(ctx context.Context, ip string, talosconfig []byte) error {
	defer close(p.dialed)
	if err := talos.PortErr(ctx, ip, 2*time.Second); err != nil {
		if Classify(err) == ReachNoNetwork {
			return err
		}
		return fmt.Errorf("port 50000 closed or host down")
	}
	tc, err := talos.Dial(ctx, ip, talosconfig)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	p.tc = tc
	return nil
}

func (p *talosProbe) probe(ctx context.Context, ip string, talosconfig []byte) (version, stage string, err error) {
	if err := p.dial(ctx, ip, talosconfig); err != nil {
		return "", "", err
	}
	tc := p.tc
	v, err := tc.Version(tc.Context(ctx))
	if err != nil {
		return "", "", fmt.Errorf("version: %w", talos.ShortGRPC(err))
	}
	if len(v.Messages) > 0 {
		version = v.Messages[0].Version.Tag
	}
	stage, err = tc.Stage(ctx)
	if err != nil {
		return version, "", fmt.Errorf("machine status: %w", talos.ShortGRPC(err))
	}
	return version, stage, nil
}

func (m *Manager) etcdStatus(ctx context.Context, cps []config.Node, dialed func(config.Node) *talos.Client) EtcdStatus {
	var e EtcdStatus
	for _, cp := range cps {
		tc := dialed(cp)
		if tc == nil {
			continue
		}
		members, err := tc.EtcdMemberList(tc.Context(ctx), &machineapi.EtcdMemberListRequest{})
		if err != nil {
			continue
		}
		for _, msg := range members.Messages {
			e.Members = len(msg.Members)
		}
		if status, err := tc.EtcdStatus(tc.Context(ctx)); err == nil {
			for _, msg := range status.Messages {
				if msg.MemberStatus != nil && msg.MemberStatus.Leader == msg.MemberStatus.MemberId {
					e.Leader = cp.Hostname
				}
				for _, ms := range msg.MemberStatus.GetErrors() {
					e.Alarms = append(e.Alarms, ms)
				}
			}
		}
		if alarms, err := tc.EtcdAlarmList(tc.Context(ctx)); err == nil {
			for _, msg := range alarms.Messages {
				for _, a := range msg.MemberAlarms {
					e.Alarms = append(e.Alarms, a.Alarm.String())
				}
			}
		}
		healthy, _ := tc.ServiceHealthy(ctx, "etcd")
		e.Healthy = healthy && len(e.Alarms) == 0
		break
	}
	return e
}
