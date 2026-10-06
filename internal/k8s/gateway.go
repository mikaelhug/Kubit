package k8s

import (
	"context"
	"fmt"
	"sort"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var httpRouteGVR = schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes"}

type Route struct {
	Namespace string      `json:"namespace"`
	Name      string      `json:"name"`
	Hostnames []string    `json:"hostnames"`
	Gateways  []string    `json:"gateways"`
	Rules     []RouteRule `json:"rules"`
	Accepted  string      `json:"accepted"`
	Message   string      `json:"message,omitempty"`
	Age       string      `json:"age"`
	AgeSec    int64       `json:"ageSec"`
	CreatedAt string      `json:"createdAt,omitempty"`
}

type RouteRule struct {
	Path     string   `json:"path"`
	Backends []string `json:"backends"`
}

func (c *Client) HTTPRoutes(ctx context.Context) ([]Route, error) {
	dyn, err := c.dynClient()
	if err != nil {
		return nil, err
	}
	list, err := dyn.Resource(httpRouteGVR).List(ctx, metav1.ListOptions{})
	if apierrors.IsNotFound(err) {
		return []Route{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]Route, 0, len(list.Items))
	for _, u := range list.Items {
		out = append(out, routeOf(u))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Namespace+"/"+out[i].Name < out[j].Namespace+"/"+out[j].Name })
	return out, nil
}

func routeOf(u unstructured.Unstructured) Route {
	r := Route{Namespace: u.GetNamespace(), Name: u.GetName(), Hostnames: []string{}, Gateways: []string{}, Rules: []RouteRule{}, Accepted: "Unknown", CreatedAt: createdAt(u.GetCreationTimestamp())}
	r.Age, r.AgeSec = age(u.GetCreationTimestamp())
	r.Hostnames, _, _ = unstructured.NestedStringSlice(u.Object, "spec", "hostnames")
	if r.Hostnames == nil {
		r.Hostnames = []string{}
	}
	parents, _, _ := unstructured.NestedSlice(u.Object, "spec", "parentRefs")
	for _, p := range parents {
		m, _ := p.(map[string]any)
		name, _ := m["name"].(string)
		if ns, _ := m["namespace"].(string); ns != "" && ns != r.Namespace {
			name = ns + "/" + name
		}
		r.Gateways = append(r.Gateways, name)
	}
	rules, _, _ := unstructured.NestedSlice(u.Object, "spec", "rules")
	for _, x := range rules {
		m, _ := x.(map[string]any)
		rule := RouteRule{Path: "/", Backends: []string{}}
		if matches, _ := m["matches"].([]any); len(matches) > 0 {
			if mm, _ := matches[0].(map[string]any); mm != nil {
				if v, _, _ := unstructured.NestedString(mm, "path", "value"); v != "" {
					rule.Path = v
				}
			}
		}
		refs, _ := m["backendRefs"].([]any)
		for _, b := range refs {
			bm, _ := b.(map[string]any)
			name, _ := bm["name"].(string)
			if port, ok := bm["port"]; ok {
				name = fmt.Sprintf("%s:%v", name, port)
			}
			rule.Backends = append(rule.Backends, name)
		}
		r.Rules = append(r.Rules, rule)
	}
	statusParents, _, _ := unstructured.NestedSlice(u.Object, "status", "parents")
	for _, p := range statusParents {
		m, _ := p.(map[string]any)
		conds, _ := m["conditions"].([]any)
		for _, c := range conds {
			cm, _ := c.(map[string]any)
			if cm["type"] == "Accepted" {
				r.Accepted, _ = cm["status"].(string)
				if r.Accepted != "True" {
					r.Message, _ = cm["message"].(string)
				}
			}
		}
	}
	return r
}
