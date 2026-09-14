package k8s

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
)

// NodeUsage is live CPU (millicores) and memory (bytes) from metrics-server.
type NodeUsage struct {
	CPUMilli    int64
	MemoryBytes int64
}

// NodeUsages returns metrics-server readings keyed by node name; an empty map (no
// error) means metrics-server is not installed or not ready yet.
func (c *Client) NodeUsages(ctx context.Context) (map[string]NodeUsage, error) {
	mc, err := metricsclient.NewForConfig(c.rest)
	if err != nil {
		return nil, err
	}
	list, err := mc.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return map[string]NodeUsage{}, nil
	}
	out := make(map[string]NodeUsage, len(list.Items))
	for _, m := range list.Items {
		out[m.Name] = NodeUsage{CPUMilli: m.Usage.Cpu().MilliValue(), MemoryBytes: m.Usage.Memory().Value()}
	}
	return out, nil
}

// podUsages returns metrics-server pod readings keyed by namespace/name.
func (c *Client) podUsages(ctx context.Context) (map[string]NodeUsage, error) {
	mc, err := metricsclient.NewForConfig(c.rest)
	if err != nil {
		return nil, err
	}
	list, err := mc.MetricsV1beta1().PodMetricses("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return map[string]NodeUsage{}, nil
	}
	out := make(map[string]NodeUsage, len(list.Items))
	for _, m := range list.Items {
		var u NodeUsage
		for _, ctr := range m.Containers {
			u.CPUMilli += ctr.Usage.Cpu().MilliValue()
			u.MemoryBytes += ctr.Usage.Memory().Value()
		}
		out[m.Namespace+"/"+m.Name] = u
	}
	return out, nil
}

// PodCount returns running pods per node.
func (c *Client) PodCount(ctx context.Context) (map[string]int, error) {
	pods, err := c.CoreV1().Pods("").List(ctx, metav1.ListOptions{FieldSelector: "status.phase=Running"})
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, p := range pods.Items {
		out[p.Spec.NodeName]++
	}
	return out, nil
}
