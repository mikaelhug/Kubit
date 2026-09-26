package cluster

import (
	"context"
	"time"

	"github.com/mikael/kubit/internal/k8s"
)

// ServiceHealth is the watcher's view of what runs *in* the cluster: enough to notice
// a workload that never becomes available, a pod that keeps crashing, a claim nobody
// binds, a service without backends, or a LoadBalancer pool that ran dry — without
// installing anything in the cluster.
type ServiceHealth struct {
	Workloads   []WorkloadHealth `json:"workloads"`
	Pods        []PodHealth      `json:"pods"`
	Claims      []ClaimHealth    `json:"claims"`
	Services    []ServiceRow     `json:"services"`
	Ingresses   []IngressHealth  `json:"ingresses"`
	Flux        []FluxHealth     `json:"flux,omitempty"`
	Pool        *PoolHealth      `json:"pool,omitempty"`
	MetalLB     bool             `json:"metallb"`
	CollectedAt time.Time        `json:"collectedAt"`
}

type FluxHealth struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Ready     string `json:"ready"`
	Reason    string `json:"reason,omitempty"`
	Message   string `json:"message,omitempty"`
	Suspended bool   `json:"suspended,omitempty"`
}

type WorkloadHealth struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Ready     int32  `json:"ready"`
	Desired   int32  `json:"desired"`
	Available bool   `json:"available"`
	AgeSec    int64  `json:"ageSec"`
}

type PodHealth struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Node      string `json:"node,omitempty"`
	Owner     string `json:"owner,omitempty"`
	Phase     string `json:"phase"` // pod phase, or the waiting reason (CrashLoopBackOff, ImagePullBackOff…)
	Restarts  int32  `json:"restarts"`
	AgeSec    int64  `json:"ageSec"`
}

type ClaimHealth struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Phase     string `json:"phase"`
	AgeSec    int64  `json:"ageSec"`
}

type ServiceRow struct {
	Namespace   string `json:"namespace"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	HasSelector bool   `json:"hasSelector"`
	Endpoints   int    `json:"endpoints"`
	AgeSec      int64  `json:"ageSec"`
}

type IngressHealth struct {
	Namespace  string `json:"namespace"`
	Name       string `json:"name"`
	HasAddress bool   `json:"hasAddress"`
	AgeSec     int64  `json:"ageSec"`
}

type PoolHealth struct {
	Range     string `json:"range"`
	Total     int    `json:"total"`
	Allocated int    `json:"allocated"`
}

// ServiceHealth lists workloads, pods, claims, services and ingresses across all
// namespaces. Five list calls; the watcher runs it less often than Status.
func (m *Manager) ServiceHealth(ctx context.Context, name string) (*ServiceHealth, error) {
	c, _, err := m.LoadCluster(ctx, name)
	if err != nil {
		return nil, err
	}
	kc, err := m.KubeClient(ctx, name)
	if err != nil {
		return nil, err
	}
	out := &ServiceHealth{CollectedAt: time.Now(), MetalLB: c.Spec.Platform.MetalLB.Enabled}
	wls, err := kc.Workloads(ctx)
	if err != nil {
		return nil, err
	}
	for _, w := range wls {
		out.Workloads = append(out.Workloads, WorkloadHealth{Kind: w.Kind, Namespace: w.Namespace, Name: w.Name, Ready: w.Ready, Desired: w.Desired, Available: w.Available, AgeSec: w.AgeSec})
	}
	pods, err := kc.Pods(ctx, "", "")
	if err != nil {
		return nil, err
	}
	for _, p := range pods {
		out.Pods = append(out.Pods, PodHealth{Namespace: p.Namespace, Name: p.Name, Node: p.Node, Owner: p.Owner, Phase: p.Phase, Restarts: p.Restarts, AgeSec: p.AgeSec})
	}
	st, err := kc.Storage(ctx)
	if err != nil {
		return nil, err
	}
	for _, cl := range st.Claims {
		out.Claims = append(out.Claims, ClaimHealth{Namespace: cl.Namespace, Name: cl.Name, Phase: cl.Phase, AgeSec: cl.AgeSec})
	}
	svcs, err := kc.Services(ctx)
	if err != nil {
		return nil, err
	}
	for _, s := range svcs {
		out.Services = append(out.Services, ServiceRow{Namespace: s.Namespace, Name: s.Name, Type: s.Type, HasSelector: s.Selector != "", Endpoints: s.Endpoints, AgeSec: s.AgeSec})
	}
	ings, err := kc.Ingresses(ctx)
	if err != nil {
		return nil, err
	}
	for _, i := range ings {
		out.Ingresses = append(out.Ingresses, IngressHealth{Namespace: i.Namespace, Name: i.Name, HasAddress: len(i.Addresses) > 0, AgeSec: i.AgeSec})
	}
	if c.Spec.Platform.Flux.Enabled {
		objs, err := kc.FluxObjects(ctx)
		if err != nil {
			return nil, err
		}
		for _, o := range objs {
			out.Flux = append(out.Flux, FluxHealth{Kind: o.Kind, Namespace: o.Namespace, Name: o.Name, Ready: o.Ready, Reason: o.Reason, Message: o.Message, Suspended: o.Suspended})
		}
	}
	if out.MetalLB && c.Spec.Platform.MetalLB.Range != "" {
		if pool, err := k8s.PoolUsageFor(c.Spec.Platform.MetalLB.Range, svcs); err == nil {
			out.Pool = &PoolHealth{Range: pool.Range, Total: pool.Total, Allocated: len(pool.Allocated)}
		}
	}
	return out, nil
}
