package k8s

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubectl/pkg/drain"
)

type NodeDetail struct {
	Name           string            `json:"name"`
	Ready          bool              `json:"ready"`
	Unschedulable  bool              `json:"unschedulable"`
	KubeletVersion string            `json:"kubeletVersion"`
	Runtime        string            `json:"containerRuntime"`
	Kernel         string            `json:"kernel"`
	OSImage        string            `json:"osImage"`
	InternalIP     string            `json:"internalIP"`
	Conditions     []Condition       `json:"conditions"`
	Taints         []string          `json:"taints"`
	Labels         map[string]string `json:"labels"`
	Capacity       Resources         `json:"capacity"`
	Allocatable    Resources         `json:"allocatable"`
	Requests       Resources         `json:"requests"`
	Pods           []PodSummary      `json:"pods"`
}

type Condition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
	Since   string `json:"since,omitempty"`
}

type Resources struct {
	CPUMilli int64 `json:"cpuMilli"`
	MemBytes int64 `json:"memBytes"`
	Pods     int64 `json:"pods"`
}

type PodSummary struct {
	Namespace  string   `json:"namespace"`
	Name       string   `json:"name"`
	Node       string   `json:"node,omitempty"`
	Containers []string `json:"containers,omitempty"`
	Phase      string   `json:"phase"`
	Ready      string   `json:"ready"`
	Restarts   int32    `json:"restarts"`
	Owner      string   `json:"owner,omitempty"`
	CPUMilli   int64    `json:"cpuMilli"`
	MemBytes   int64    `json:"memBytes"`
	Age        string   `json:"age"`
	AgeSec     int64    `json:"ageSec"`
	CreatedAt  string   `json:"createdAt,omitempty"`
	UsageCPU   int64    `json:"usageCpuMilli,omitempty"`
	UsageMem   int64    `json:"usageMemBytes,omitempty"`
}

func (c *Client) NodeDetail(ctx context.Context, name string) (*NodeDetail, error) {
	n, err := c.node(ctx, name)
	if err != nil {
		return nil, err
	}
	s := statusOf(n)
	d := &NodeDetail{
		Name: n.Name, Ready: s.Ready, Unschedulable: s.Unschedulable, KubeletVersion: s.KubeletVersion,
		Runtime: n.Status.NodeInfo.ContainerRuntimeVersion, Kernel: n.Status.NodeInfo.KernelVersion, OSImage: s.OSImage,
		InternalIP: s.InternalIP, Labels: n.Labels,
		Conditions: []Condition{}, Taints: []string{}, Pods: []PodSummary{},
		Capacity:    Resources{CPUMilli: s.CapacityCPU, MemBytes: s.CapacityMem, Pods: s.CapacityPods},
		Allocatable: Resources{CPUMilli: s.AllocatableCPU, MemBytes: s.AllocatableMem, Pods: s.CapacityPods},
	}
	if d.Labels == nil {
		d.Labels = map[string]string{}
	}
	for _, cond := range n.Status.Conditions {
		d.Conditions = append(d.Conditions, Condition{Type: string(cond.Type), Status: string(cond.Status), Reason: cond.Reason, Message: cond.Message, Since: cond.LastTransitionTime.UTC().Format("2006-01-02T15:04:05Z")})
	}
	for _, t := range n.Spec.Taints {
		d.Taints = append(d.Taints, fmt.Sprintf("%s=%s:%s", t.Key, t.Value, t.Effect))
	}
	pods, err := c.pods(ctx, "", metav1.ListOptions{FieldSelector: "spec.nodeName=" + name})
	if err != nil {
		return nil, err
	}
	usage, _ := c.podUsages(ctx)
	for _, p := range pods {
		ps := podSummary(&p)
		if p.Status.Phase == corev1.PodRunning {
			d.Requests.CPUMilli += ps.CPUMilli
			d.Requests.MemBytes += ps.MemBytes
			d.Requests.Pods++
		}
		if u, ok := usage[p.Namespace+"/"+p.Name]; ok {
			ps.UsageCPU, ps.UsageMem = u.CPUMilli, u.MemoryBytes
		}
		d.Pods = append(d.Pods, ps)
	}
	sort.Slice(d.Pods, func(i, j int) bool {
		if d.Pods[i].Namespace != d.Pods[j].Namespace {
			return d.Pods[i].Namespace < d.Pods[j].Namespace
		}
		return d.Pods[i].Name < d.Pods[j].Name
	})
	return d, nil
}

func (c *Client) Cordon(ctx context.Context, name string) error {
	node, err := c.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	return drain.RunCordonOrUncordon(&drain.Helper{Ctx: ctx, Client: c.Interface}, node, true)
}
