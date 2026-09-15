package k8s

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Service struct {
	Namespace   string   `json:"namespace"`
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	ClusterIP   string   `json:"clusterIP"`
	ExternalIPs []string `json:"externalIPs,omitempty"`
	Ports       []string `json:"ports"`
	Endpoints   int      `json:"endpoints"` // ready addresses behind the service
	Selector    string   `json:"selector,omitempty"`
	Age         string   `json:"age"`
	AgeSec      int64    `json:"ageSec"`
}

type Ingress struct {
	Namespace string        `json:"namespace"`
	Name      string        `json:"name"`
	Class     string        `json:"class,omitempty"`
	Rules     []IngressRule `json:"rules"`
	Addresses []string      `json:"addresses,omitempty"`
	TLSHosts  []string      `json:"tlsHosts,omitempty"`
	Age       string        `json:"age"`
	AgeSec    int64         `json:"ageSec"`
}

type IngressRule struct {
	Host    string `json:"host"`
	Path    string `json:"path"`
	Service string `json:"service"`
	Port    string `json:"port"`
}

// PoolUsage maps every address of a MetalLB range to the service holding it.
type PoolUsage struct {
	Range     string      `json:"range"`
	Total     int         `json:"total"`
	Allocated []PoolAlloc `json:"allocated"`
}

type PoolAlloc struct {
	IP      string `json:"ip"`
	Service string `json:"service"` // namespace/name
}

func (c *Client) Services(ctx context.Context) ([]Service, error) {
	list, err := c.CoreV1().Services("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	eps, _ := c.DiscoveryV1().EndpointSlices("").List(ctx, metav1.ListOptions{})
	ready := map[string]int{}
	if eps != nil {
		for _, s := range eps.Items {
			svc := s.Labels["kubernetes.io/service-name"]
			for _, e := range s.Endpoints {
				if e.Conditions.Ready == nil || *e.Conditions.Ready {
					ready[s.Namespace+"/"+svc] += len(e.Addresses)
				}
			}
		}
	}
	out := make([]Service, 0, len(list.Items))
	for _, s := range list.Items {
		sv := Service{Namespace: s.Namespace, Name: s.Name, Type: string(s.Spec.Type), ClusterIP: s.Spec.ClusterIP, Age: metav1.Now().Sub(s.CreationTimestamp.Time).Truncate(1e9).String(), AgeSec: int64(metav1.Now().Sub(s.CreationTimestamp.Time).Seconds()), Endpoints: ready[s.Namespace+"/"+s.Name]}
		for _, ing := range s.Status.LoadBalancer.Ingress {
			if ing.IP != "" {
				sv.ExternalIPs = append(sv.ExternalIPs, ing.IP)
			}
		}
		sv.ExternalIPs = append(sv.ExternalIPs, s.Spec.ExternalIPs...)
		for _, p := range s.Spec.Ports {
			ps := fmt.Sprintf("%d/%s", p.Port, p.Protocol)
			if p.NodePort > 0 {
				ps += fmt.Sprintf(" (node %d)", p.NodePort)
			}
			sv.Ports = append(sv.Ports, ps)
		}
		var sel []string
		for k, v := range s.Spec.Selector {
			sel = append(sel, k+"="+v)
		}
		sort.Strings(sel)
		sv.Selector = strings.Join(sel, ",")
		out = append(out, sv)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Namespace+"/"+out[i].Name < out[j].Namespace+"/"+out[j].Name })
	return out, nil
}

func (c *Client) Ingresses(ctx context.Context) ([]Ingress, error) {
	list, err := c.NetworkingV1().Ingresses("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]Ingress, 0, len(list.Items))
	for _, ing := range list.Items {
		i := Ingress{Namespace: ing.Namespace, Name: ing.Name, Age: metav1.Now().Sub(ing.CreationTimestamp.Time).Truncate(1e9).String(), AgeSec: int64(metav1.Now().Sub(ing.CreationTimestamp.Time).Seconds()), Rules: []IngressRule{}}
		if ing.Spec.IngressClassName != nil {
			i.Class = *ing.Spec.IngressClassName
		}
		for _, r := range ing.Spec.Rules {
			if r.HTTP == nil {
				i.Rules = append(i.Rules, IngressRule{Host: r.Host})
				continue
			}
			for _, p := range r.HTTP.Paths {
				rule := IngressRule{Host: r.Host, Path: p.Path}
				if p.Backend.Service != nil {
					rule.Service = p.Backend.Service.Name
					if p.Backend.Service.Port.Name != "" {
						rule.Port = p.Backend.Service.Port.Name
					} else {
						rule.Port = fmt.Sprint(p.Backend.Service.Port.Number)
					}
				}
				i.Rules = append(i.Rules, rule)
			}
		}
		for _, a := range ing.Status.LoadBalancer.Ingress {
			if a.IP != "" {
				i.Addresses = append(i.Addresses, a.IP)
			} else if a.Hostname != "" {
				i.Addresses = append(i.Addresses, a.Hostname)
			}
		}
		for _, t := range ing.Spec.TLS {
			i.TLSHosts = append(i.TLSHosts, t.Hosts...)
		}
		out = append(out, i)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Namespace+"/"+out[i].Name < out[j].Namespace+"/"+out[j].Name })
	return out, nil
}

// PoolUsageFor computes which addresses of a "a.b.c.d-a.b.c.e" range are held by
// LoadBalancer services.
func PoolUsageFor(rangeSpec string, services []Service) (*PoolUsage, error) {
	from, to, ok := strings.Cut(rangeSpec, "-")
	if !ok {
		return nil, fmt.Errorf("range %q is not start-end", rangeSpec)
	}
	a, err := netip.ParseAddr(strings.TrimSpace(from))
	if err != nil {
		return nil, err
	}
	b, err := netip.ParseAddr(strings.TrimSpace(to))
	if err != nil {
		return nil, err
	}
	held := map[string]string{}
	for _, s := range services {
		if s.Type != string(corev1.ServiceTypeLoadBalancer) {
			continue
		}
		for _, ip := range s.ExternalIPs {
			held[ip] = s.Namespace + "/" + s.Name
		}
	}
	u := &PoolUsage{Range: rangeSpec, Allocated: []PoolAlloc{}}
	for ip := a; !b.Less(ip); ip = ip.Next() {
		u.Total++
		if svc, ok := held[ip.String()]; ok {
			u.Allocated = append(u.Allocated, PoolAlloc{IP: ip.String(), Service: svc})
		}
		if u.Total > 65536 {
			break
		}
	}
	return u, nil
}
