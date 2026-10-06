package k8s

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestRouteOfFollowsGatewayAPIDefaults(t *testing.T) {
	u := unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "web", "namespace": "shop"},
		"spec": map[string]any{
			"hostnames":  []any{"shop.example.com"},
			"parentRefs": []any{map[string]any{"name": "traefik-gateway", "namespace": "traefik"}},
			"rules": []any{
				map[string]any{"matches": []any{map[string]any{"path": map[string]any{"type": "PathPrefix", "value": "/api"}}}, "backendRefs": []any{map[string]any{"name": "api", "port": int64(8080)}}},
				map[string]any{"backendRefs": []any{map[string]any{"name": "web", "port": int64(80)}}},
			},
		},
		"status": map[string]any{"parents": []any{map[string]any{"conditions": []any{map[string]any{"type": "Accepted", "status": "False", "message": "no listener"}}}}},
	}}
	r := routeOf(u)
	if r.Gateways[0] != "traefik/traefik-gateway" || r.Rules[1].Path != "/" || r.Accepted != "False" {
		t.Errorf("route %+v", r)
	}
}
