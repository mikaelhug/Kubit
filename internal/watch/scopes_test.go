package watch

import (
	"slices"
	"testing"
)

func TestKubeScopesAddsAddonsForAddonNamespaces(t *testing.T) {
	for _, c := range []struct {
		scope, ns string
		want      []string
	}{
		{"workloads", "metallb-system", []string{"workloads", "addons"}},
		{"workloads", "longhorn-system", []string{"workloads", "addons"}},
		{"workloads", "kube-system", []string{"workloads"}},
		{"workloads", "default", []string{"workloads"}},
		{"workloads", "", []string{"workloads"}},
		{"network", "ingress-nginx", []string{"network"}},
		{"nodes", "", []string{"nodes"}},
	} {
		if got := kubeScopes(c.scope, c.ns); !slices.Equal(got, c.want) {
			t.Errorf("kubeScopes(%q, %q) = %v, want %v", c.scope, c.ns, got, c.want)
		}
	}
}
