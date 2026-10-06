package cluster

import (
	"testing"

	"github.com/mikael/kubit/internal/k8s"
)

func TestADisabledChartlessAddonStillRunningIsOrphaned(t *testing.T) {
	st := AddonStatus{Enabled: false, Readiness: &k8s.Readiness{Ready: 2, Total: 2}}
	if got := addonState(st, false, true); got != "orphaned" {
		t.Errorf("%s, want orphaned", got)
	}
}
