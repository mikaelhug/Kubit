package cluster

import (
	"context"
	"sync"
	"time"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/k8s"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
)

// Status is the dashboard view of one cluster.
type Status struct {
	Name              string                `json:"name"`
	State             string                `json:"state"`
	TalosVersion      string                `json:"talosVersion"`
	KubernetesVersion string                `json:"kubernetesVersion"`
	Endpoint          string                `json:"endpoint"`
	APIReachable      bool                  `json:"apiReachable"`
	Nodes             []NodeStatus          `json:"nodes"`
	Etcd              EtcdStatus            `json:"etcd"`
	Totals            Totals                `json:"totals"`
	Platform          *store.PlatformStatus `json:"platform,omitempty"`
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
	Stage          string `json:"stage"`
	CPUMilli       int64  `json:"cpuMilli"`
	CPUCapMilli    int64  `json:"cpuCapMilli"`
	MemBytes       int64  `json:"memBytes"`
	MemCapBytes    int64  `json:"memCapBytes"`
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

// Status gathers node, etcd and resource state. Talos and Kubernetes are queried in
// parallel with short timeouts; unreachable parts degrade to zero values rather than
// failing the whole view.
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
		p.Outputs = redact(p.Outputs)
		st.Platform = p
	}
	sec, err := m.Store.GetClusterSecrets(ctx, name)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	byHost := map[string]*NodeStatus{}
	for _, n := range orderedNodes(c) {
		ns := &NodeStatus{Hostname: n.Hostname, IP: n.IP, Role: string(n.Role), Arch: string(n.Arch), KVM: n.KVM}
		st.Nodes = append(st.Nodes, *ns)
		byHost[n.Hostname] = &st.Nodes[len(st.Nodes)-1]
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, n := range c.Spec.Nodes {
		wg.Add(1)
		go func(n config.Node) {
			defer wg.Done()
			tc, err := talos.Dial(ctx, n.IP, sec.Talosconfig)
			if err != nil {
				return
			}
			defer tc.Close()
			v, err := tc.Version(tc.Context(ctx))
			if err != nil {
				return
			}
			stage, _ := talos.Stage(ctx, n.IP, sec.Talosconfig)
			mu.Lock()
			ns := byHost[n.Hostname]
			ns.TalosReachable = true
			ns.Stage = stage
			if len(v.Messages) > 0 {
				ns.TalosVersion = v.Messages[0].Version.Tag
			}
			mu.Unlock()
		}(n)
	}
	if cps := c.ControlPlanes(); len(cps) > 0 && sec.Kubeconfig != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e := m.etcdStatus(ctx, cps, sec.Talosconfig)
			mu.Lock()
			st.Etcd = e
			mu.Unlock()
		}()
	}
	if sec.Kubeconfig != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			kc, err := k8s.New(sec.Kubeconfig)
			if err != nil {
				return
			}
			nodes, err := kc.Nodes(ctx)
			if err != nil {
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
				ns.Ready = kn.Ready
				ns.Unschedulable = kn.Unschedulable
				ns.KubeletVersion = kn.KubeletVersion
				ns.CPUCapMilli = kn.AllocatableCPU
				ns.MemCapBytes = kn.AllocatableMem
				ns.PodCap = kn.CapacityPods
				ns.GVisor = kn.Labels[config.LabelGVisor] == "true"
				ns.CPUMilli = usage[kn.Name].CPUMilli
				ns.MemBytes = usage[kn.Name].MemoryBytes
				ns.Pods = pods[kn.Name]
			}
		}()
	}
	wg.Wait()

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
	return st, nil
}

func (m *Manager) etcdStatus(ctx context.Context, cps []config.Node, talosconfig []byte) EtcdStatus {
	var e EtcdStatus
	for _, cp := range cps {
		tc, err := talos.Dial(ctx, cp.IP, talosconfig)
		if err != nil {
			continue
		}
		members, err := tc.EtcdMemberList(tc.Context(ctx), &machineapi.EtcdMemberListRequest{})
		if err != nil {
			tc.Close()
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
		tc.Close()
		e.Healthy = healthy && len(e.Alarms) == 0
		break
	}
	return e
}

func redact(outputs map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range outputs {
		if k == "argocd_admin_password" {
			continue
		}
		out[k] = v
	}
	return out
}
