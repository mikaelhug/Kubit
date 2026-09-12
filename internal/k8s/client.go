// Package k8s is Kubit's view of a cluster through the Kubernetes API: readiness,
// cordon/drain and metrics. Platform add-ons are OpenTofu's job, not this package's.
package k8s

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type Client struct {
	*kubernetes.Clientset
	rest *rest.Config
}

func New(kubeconfig []byte) (*Client, error) {
	cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("kubeconfig: %w", err)
	}
	cfg.Timeout = 15 * time.Second
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &Client{Clientset: cs, rest: cfg}, nil
}

type NodeStatus struct {
	Name           string
	Ready          bool
	Unschedulable  bool
	KubeletVersion string
	OSImage        string
	InternalIP     string
	Labels         map[string]string
	AllocatableCPU int64 // millicores
	AllocatableMem int64 // bytes
	CapacityPods   int64
}

func (c *Client) Nodes(ctx context.Context) ([]NodeStatus, error) {
	list, err := c.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]NodeStatus, 0, len(list.Items))
	for _, n := range list.Items {
		out = append(out, statusOf(&n))
	}
	return out, nil
}

func statusOf(n *corev1.Node) NodeStatus {
	s := NodeStatus{
		Name: n.Name, Unschedulable: n.Spec.Unschedulable,
		KubeletVersion: n.Status.NodeInfo.KubeletVersion, OSImage: n.Status.NodeInfo.OSImage,
		Labels: n.Labels,
	}
	s.AllocatableCPU = n.Status.Allocatable.Cpu().MilliValue()
	s.AllocatableMem = n.Status.Allocatable.Memory().Value()
	s.CapacityPods = n.Status.Capacity.Pods().Value()
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
			s.Ready = true
		}
	}
	for _, a := range n.Status.Addresses {
		if a.Type == corev1.NodeInternalIP {
			s.InternalIP = a.Address
		}
	}
	return s
}

// WaitReady blocks until every named node is registered and Ready. progress is called
// whenever the set of ready nodes grows.
func (c *Client) WaitReady(ctx context.Context, names []string, timeout time.Duration, progress func(ready, total int)) error {
	deadline := time.Now().Add(timeout)
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	lastReady := -1
	for {
		ready := 0
		if nodes, err := c.Nodes(ctx); err == nil {
			for _, n := range nodes {
				if want[n.Name] && n.Ready {
					ready++
				}
			}
		}
		if ready != lastReady && progress != nil {
			progress(ready, len(want))
			lastReady = ready
		}
		if ready == len(want) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%d/%d nodes Ready after %s", ready, len(want), timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}
