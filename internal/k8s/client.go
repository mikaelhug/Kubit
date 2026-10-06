package k8s

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
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
	cfg.QPS, cfg.Burst = 50, 100
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
	list, err := c.nodes(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]NodeStatus, 0, len(list))
	for _, n := range list {
		out = append(out, statusOf(&n))
	}
	return out, nil
}

func (c *Client) Ready(ctx context.Context) error {
	return c.Discovery().RESTClient().Get().AbsPath("/readyz").Do(ctx).Error()
}

func (c *Client) LoadBalancerIP(ctx context.Context, namespace, name string) string {
	svcs, err := c.services(ctx)
	if err != nil {
		return ""
	}
	for _, svc := range svcs {
		if svc.Namespace != namespace || svc.Name != name {
			continue
		}
		for _, in := range svc.Status.LoadBalancer.Ingress {
			if in.IP != "" {
				return in.IP
			}
		}
	}
	return ""
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
	if len(names) == 0 {
		return nil
	}
	wait, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	ok := map[string]bool{}
	ready, lastReady := 0, -1
	see := func(n *corev1.Node, gone bool) bool {
		if want[n.Name] {
			ok[n.Name] = !gone && done(statusOf(n))
		}
		ready = 0
		for n := range want {
			if ok[n] {
				ready++
			}
		}
		if ready != lastReady && progress != nil {
			progress(ready, len(want))
		}
		lastReady = ready
		return ready == len(want)
	}
	var last error
	for {
		finished, err := c.watchNodes(wait, see)
		if finished {
			return nil
		}
		if err != nil && wait.Err() == nil {
			last = err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if wait.Err() != nil {
			if last != nil {
				return fmt.Errorf("%d/%d nodes Ready after %s: %w", ready, len(want), timeout, last)
			}
			return fmt.Errorf("%d/%d nodes Ready after %s", ready, len(want), timeout)
		}
		if err == nil {
			continue
		}
		select {
		case <-wait.Done():
		case <-time.After(2 * time.Second):
		}
	}
}

func (c *Client) watchNodes(ctx context.Context, see func(n *corev1.Node, gone bool) bool) (bool, error) {
	list, err := c.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, err
	}
	finished := false
	for i := range list.Items {
		finished = see(&list.Items[i], false)
	}
	if finished {
		return true, nil
	}
	w, err := c.CoreV1().Nodes().Watch(ctx, metav1.ListOptions{ResourceVersion: list.ResourceVersion, TimeoutSeconds: new(int64(10))})
	if err != nil {
		return false, err
	}
	defer w.Stop()
	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case ev, open := <-w.ResultChan():
			if !open {
				return false, nil
			}
			switch o := ev.Object.(type) {
			case *corev1.Node:
				if see(o, ev.Type == watch.Deleted) {
					return true, nil
				}
			case *metav1.Status:
				return false, fmt.Errorf("watch nodes: %s", o.Message)
			}
		}
	}
}

func (c *Client) ResetDiscovery() { c.restMapper().Reset() }

func (c *Client) WatchLease(ctx context.Context, namespace, name, resourceVersion string) (watch.Interface, error) {
	cs, err := c.streamClient()
	if err != nil {
		return nil, err
	}
	return cs.CoordinationV1().Leases(namespace).Watch(ctx, metav1.ListOptions{FieldSelector: "metadata.name=" + name, ResourceVersion: resourceVersion})
}
