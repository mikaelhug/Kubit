package cluster

import (
	"testing"

	"github.com/mikaelhug/kubit/internal/config"
	"github.com/mikaelhug/kubit/internal/store"
	"github.com/mikaelhug/kubit/internal/talos"
)

func TestATalosVIPIsNotRecordedAsAMachine(t *testing.T) {
	st := store.New()
	lab := &config.Cluster{Metadata: config.Metadata{Name: "lab"}}
	lab.Spec.ControlPlane.VIP = "10.0.0.250"
	st.PutCluster(store.ClusterRow{Name: "lab", Spec: lab, State: StateReady})
	inv := &talos.Inventory{Links: []talos.Link{{Name: "eth0", MAC: "52:54:00:00:00:01", Up: true}}}
	RecordScan(st, []talos.ScanResult{
		{IP: "10.0.0.5", State: talos.StateMaintenance, Inventory: inv},
		{IP: "10.0.0.250", State: talos.StateMaintenance, Inventory: inv},
	})
	if _, err := st.GetNode("10.0.0.5"); err != nil {
		t.Fatalf("the machine: %v", err)
	}
	if _, err := st.GetNode("10.0.0.250"); err == nil {
		t.Error("a cluster VIP must not be recorded as a machine")
	}
}
