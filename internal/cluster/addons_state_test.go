package cluster

import (
	"testing"

	"github.com/mikael/kubit/internal/k8s"
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
