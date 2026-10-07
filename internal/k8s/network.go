package k8s

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/mikaelhug/kubit/internal/config"
	corev1 "k8s.io/api/core/v1"
)

type Service struct {
	Namespace   string   `json:"namespace"`
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	ClusterIP   string   `json:"clusterIP"`
	ExternalIPs []string `json:"externalIPs,omitempty"`
	Ports       []string `json:"ports"`
	Endpoints   int      `json:"endpoints"`
	Selector    string   `json:"selector,omitempty"`
	Age         string   `json:"age"`
	AgeSec      int64    `json:"ageSec"`
	CreatedAt   string   `json:"createdAt,omitempty"`
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
	CreatedAt string        `json:"createdAt,omitempty"`
}

type IngressRule struct {
	Host    string `json:"host"`
	Path    string `json:"path"`
	Service string `json:"service"`
	Port    string `json:"port"`
}

type PoolUsage struct {
	Range     string      `json:"range"`
	Total     int         `json:"total"`
	Allocated []PoolAlloc `json:"allocated"`
}

type PoolAlloc struct {
	IP      string `json:"ip"`
	Service string `json:"service"`
}

func (c *Client) Services(ctx context.Context) ([]Service, error) {
	list, err := c.services(ctx)
	if err != nil {
		return nil, err
	}
	eps, err := c.endpointSlices(ctx)
	if err != nil {
		return nil, err
	}
	ready := map[string]int{}
	for _, s := range eps {
		svc := s.Labels["kubernetes.io/service-name"]
		for _, e := range s.Endpoints {
			if e.Conditions.Ready == nil || *e.Conditions.Ready {
				ready[s.Namespace+"/"+svc] += len(e.Addresses)
			}
		}
	}
	out := make([]Service, 0, len(list))
	for _, s := range list {
		sv := Service{Namespace: s.Namespace, Name: s.Name, Type: string(s.Spec.Type), ClusterIP: s.Spec.ClusterIP, Ports: []string{}, Endpoints: ready[s.Namespace+"/"+s.Name], CreatedAt: createdAt(s.CreationTimestamp)}
		sv.Age, sv.AgeSec = age(s.CreationTimestamp)
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
	list, err := c.ingresses(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Ingress, 0, len(list))
	for _, ing := range list {
		i := Ingress{Namespace: ing.Namespace, Name: ing.Name, Rules: []IngressRule{}, CreatedAt: createdAt(ing.CreationTimestamp)}
		i.Age, i.AgeSec = age(ing.CreationTimestamp)
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

func PoolUsageFor(rangeSpec string, services []Service) (*PoolUsage, error) {
	a, b, err := config.ParseIPRange(rangeSpec)
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
