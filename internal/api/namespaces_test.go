package api

import (
	"testing"

	"github.com/mikael/kubit/internal/k8s"
)

func TestNamespaceRows(t *testing.T) {
	rows := namespaceRows([]k8s.Namespace{{Name: "flux-system"}, {Name: "default"}, {Name: "kube-node-lease"}, {Name: "kube-system"}, {Name: "longhorn-system"}, {Name: "metallb-system"}, {Name: "podinfo"}})
	want := map[string]string{"flux-system": "flux", "kube-node-lease": "kubernetes", "kube-system": "kubernetes", "longhorn-system": "longhorn", "metallb-system": "metallb"}
	for _, r := range rows {
		addon, platform := want[r.Name]
		if r.Platform != platform || r.Addon != addon {
			t.Errorf("%s: platform=%v addon=%q", r.Name, r.Platform, r.Addon)
		}
	}
}
