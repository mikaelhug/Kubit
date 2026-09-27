package k8s

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
)

type Client struct {
	kubernetes.Interface
	rest *rest.Config

	dynOnce sync.Once
	dyn     dynamic.Interface
	dynErr  error

	metricsOnce sync.Once
	metrics     metricsclient.Interface
	metricsErr  error

	mapperOnce sync.Once
	mapper     *restmapper.DeferredDiscoveryRESTMapper

	cache atomic.Pointer[Cache]
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
	return &Client{Interface: cs, rest: cfg}, nil
}

func (c *Client) dynClient() (dynamic.Interface, error) {
	c.dynOnce.Do(func() { c.dyn, c.dynErr = dynamic.NewForConfig(c.rest) })
	return c.dyn, c.dynErr
}

func (c *Client) metricsClient() (metricsclient.Interface, error) {
	c.metricsOnce.Do(func() { c.metrics, c.metricsErr = metricsclient.NewForConfig(c.rest) })
	return c.metrics, c.metricsErr
}

func (c *Client) restMapper() *restmapper.DeferredDiscoveryRESTMapper {
	c.mapperOnce.Do(func() {
		c.mapper = restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(c.Discovery()))
	})
	return c.mapper
}

func (c *Client) streamConfig() *rest.Config {
	cfg := rest.CopyConfig(c.rest)
	cfg.Timeout = 0
	return cfg
}

func (c *Client) streamClient() (*kubernetes.Clientset, error) {
	return kubernetes.NewForConfig(c.streamConfig())
}

type NodeStatus struct {
	Name           string
	Ready          bool
	Unschedulable  bool
	KubeletVersion string
	OSImage        string
	InternalIP     string
	BootID         string
	Labels         map[string]string
	AllocatableCPU int64
	AllocatableMem int64
	CapacityCPU    int64
	CapacityMem    int64
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
		BootID: n.Status.NodeInfo.BootID, Labels: n.Labels,
	}
	s.AllocatableCPU = n.Status.Allocatable.Cpu().MilliValue()
	s.AllocatableMem = n.Status.Allocatable.Memory().Value()
	s.CapacityCPU = n.Status.Capacity.Cpu().MilliValue()
	s.CapacityMem = n.Status.Capacity.Memory().Value()
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

func (c *Client) NodeBootIDs(ctx context.Context, names ...string) (map[string]string, error) {
	out := map[string]string{}
	for _, name := range names {
		n, err := c.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		out[name] = n.Status.NodeInfo.BootID
	}
	return out, nil
}

func (c *Client) WaitReady(ctx context.Context, names []string, timeout time.Duration, progress func(ready, total int)) error {
	return c.WaitNodes(ctx, names, timeout, func(n NodeStatus) bool { return n.Ready }, progress)
}

func (c *Client) WaitRebooted(ctx context.Context, before map[string]string, timeout time.Duration) error {
	names := make([]string, 0, len(before))
	for name := range before {
		names = append(names, name)
	}
	return c.WaitNodes(ctx, names, timeout, func(n NodeStatus) bool {
		return n.Ready && (before[n.Name] == "" || n.BootID != before[n.Name])
	}, nil)
}

func (c *Client) WaitNodes(ctx context.Context, names []string, timeout time.Duration, done func(NodeStatus) bool, progress func(ready, total int)) error {
	deadline := time.Now().Add(timeout)
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	lastReady := -1
	for {
		ready := 0
		nodes, err := c.nodes(ctx)
		for i := range nodes {
			if want[nodes[i].Name] && done(statusOf(&nodes[i])) {
				ready++
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
			if err != nil {
				return fmt.Errorf("%d/%d nodes Ready after %s: %w", ready, len(want), timeout, err)
			}
			return fmt.Errorf("%d/%d nodes Ready after %s", ready, len(want), timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

func (c *Client) ResetDiscovery() { c.restMapper().Reset() }
