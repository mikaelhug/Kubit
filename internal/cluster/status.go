package cluster

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/k8s"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	"google.golang.org/grpc/status"
)

// Status is the dashboard view of one cluster.
type Status struct {
	Name              string `json:"name"`
	State             string `json:"state"`
	TalosVersion      string `json:"talosVersion"`
	KubernetesVersion string `json:"kubernetesVersion"`
	Endpoint          string `json:"endpoint"`
	APIReachable      bool   `json:"apiReachable"`
	// APIError says why the Kubernetes API could not be queried.
	APIError string                `json:"apiError,omitempty"`
	Nodes    []NodeStatus          `json:"nodes"`
	Etcd     EtcdStatus            `json:"etcd"`
	Totals   Totals                `json:"totals"`
	Platform *store.PlatformStatus `json:"platform,omitempty"`
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
	// TalosError is the dial/query failure when the Talos API did not answer.
	TalosError string `json:"talosError,omitempty"`
	// Registered is true once the kubelet has created its Node object.
	Registered bool `json:"registered"`
	// Pool is the node's pool; SeenAt is set when discovery last saw the machine on a
	// different address than the one declared (DHCP lease moved).
	Pool        string `json:"pool"`
	SeenAt      string `json:"seenAt,omitempty"`
	Stage       string `json:"stage"`
	CPUMilli    int64  `json:"cpuMilli"`
	CPUCapMilli int64  `json:"cpuCapMilli"`
	MemBytes    int64  `json:"memBytes"`
	MemCapBytes int64  `json:"memCapBytes"`
	Pods        int    `json:"pods"`
	PodCap      int64  `json:"podCap"`
	GVisor      bool   `json:"gvisor"`
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

	ordered := orderedNodes(c)
	st.Nodes = make([]NodeStatus, len(ordered))
	byHost := map[string]*NodeStatus{}
	for i, n := range ordered {
		st.Nodes[i] = NodeStatus{Hostname: n.Hostname, IP: n.IP, Role: string(n.Role), Pool: n.Pool, Arch: string(n.Arch), KVM: n.KVM}
		if n.MAC != "" {
			if mach, err := m.Store.GetMachine(ctx, n.MAC); err == nil && mach.IP != "" && mach.IP != n.IP {
				st.Nodes[i].SeenAt = mach.IP
			}
		}
		byHost[n.Hostname] = &st.Nodes[i]
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, n := range c.Spec.Nodes {
		wg.Add(1)
		go func(n config.Node) {
			defer wg.Done()
			// Each node gets its own short deadline so one dead machine cannot starve the rest.
			nctx, cancel := context.WithTimeout(ctx, 6*time.Second)
			defer cancel()
			ver, stage, err := probeNode(nctx, n.IP, sec.Talosconfig)
			mu.Lock()
			defer mu.Unlock()
			ns := byHost[n.Hostname]
			if err != nil {
				ns.TalosError = err.Error()
				return
			}
			ns.TalosReachable = true
			ns.Stage = stage
			ns.TalosVersion = ver
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
				mu.Lock()
				st.APIError = err.Error()
				mu.Unlock()
				return
			}
			nodes, err := kc.Nodes(ctx)
			if err != nil {
				mu.Lock()
				st.APIError = err.Error()
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

// probeNode asks a node for its version and stage over mTLS, wrapping failures with
// what was attempted so the UI can show the cause.
func probeNode(ctx context.Context, ip string, talosconfig []byte) (version, stage string, err error) {
	if !talos.PortOpen(ip, 2*time.Second) {
		return "", "", fmt.Errorf("port 50000 closed or host down")
	}
	tc, err := talos.Dial(ctx, ip, talosconfig)
	if err != nil {
		return "", "", fmt.Errorf("dial: %w", err)
	}
	defer tc.Close()
	v, err := tc.Version(tc.Context(ctx))
	if err != nil {
		return "", "", fmt.Errorf("version: %w", shortGRPC(err))
	}
	if len(v.Messages) > 0 {
		version = v.Messages[0].Version.Tag
	}
	stage, err = talos.Stage(ctx, ip, talosconfig)
	if err != nil {
		return version, "", fmt.Errorf("machine status: %w", shortGRPC(err))
	}
	return version, stage, nil
}

// shortGRPC strips the "rpc error: code = X desc =" prefix that hides the message.
func shortGRPC(err error) error {
	if st, ok := status.FromError(err); ok {
		return fmt.Errorf("%s (%s)", st.Message(), st.Code())
	}
	return err
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
