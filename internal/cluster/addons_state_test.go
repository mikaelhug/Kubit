package cluster

import (
	"errors"
	"testing"

	"github.com/mikael/kubit/internal/k8s"
	"github.com/mikael/kubit/internal/tofu"
)

func TestChartlessAddonState(t *testing.T) {
	running := &k8s.Readiness{Ready: 2, Total: 2}
	for _, c := range []struct {
		st   AddonStatus
		want string
	}{
		{AddonStatus{Enabled: false}, "disabled"},
		{AddonStatus{Enabled: false, Readiness: running}, "orphaned"},
		{AddonStatus{Enabled: true}, "pending"},
		{AddonStatus{Enabled: true, Readiness: running}, "ready"},
		{AddonStatus{Enabled: true, Readiness: &k8s.Readiness{Ready: 1, Total: 2}}, "degraded"},
	} {
		if got := addonState(c.st, false, true); got != c.want {
			t.Errorf("%+v: %s, want %s", c.st, got, c.want)
		}
	}
}

func TestAddonErrorsLogOncePerChange(t *testing.T) {
	m := &Manager{}
	show := errors.New("tofu show: exit 1")
	for i, c := range []struct {
		name string
		err  error
		want bool
	}{
		{"a", show, true},
		{"a", show, false},
		{"b", show, true},
		{"a", errors.New("tofu show: exit 2"), true},
		{"a", nil, false},
		{"a", nil, false},
		{"a", show, true},
	} {
		if got := m.addonErrorChanged(c.name, c.err); got != c.want {
			t.Errorf("step %d (%s, %v): %v, want %v", i, c.name, c.err, got, c.want)
		}
	}
}

func TestTraefikNamespaces(t *testing.T) {
	for ns, want := range map[string]string{"traefik": "traefik", "ingress-nginx": "traefik"} {
		if got, ok := PlatformNamespace(ns); !ok || got != want {
			t.Errorf("PlatformNamespace(%s) = %s %v, want %s", ns, got, ok, want)
		}
	}
	if !AddonNamespace("traefik") {
		t.Error("traefik is the add-on's namespace")
	}
	if AddonNamespace("ingress-nginx") {
		t.Error("the leftover ingress-nginx namespace holds no add-on workloads")
	}
	for _, a := range addonMeta {
		if a.key == "traefik" && tofu.ChartVersions[a.tofu] == "" {
			t.Errorf("no chart pin for %s", a.tofu)
		}
	}
}
