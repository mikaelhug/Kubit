package cluster

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
)

func TestRecordScanSkipsVIPsAndErrors(t *testing.T) {
	c, _ := store.NewCrypto(bytes.Repeat([]byte{3}, 32))
	st, err := store.Open(t.TempDir(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if err := st.PutCluster(ctx, store.ClusterRow{Name: "lab", Spec: []byte("spec:\n  controlPlane:\n    vip: 10.0.0.250\n"), State: StateReady}); err != nil {
		t.Fatal(err)
	}
	inv := &talos.Inventory{TalosVersion: "v1.14.1", Arch: "amd64", UUID: "u-1", Links: []talos.Link{{Name: "eth0", MAC: "52:54:00:00:00:01", Up: true}}}
	results := []talos.ScanResult{
		{IP: "10.0.0.5", State: talos.StateMaintenance, Inventory: inv},
		{IP: "10.0.0.250", State: talos.StateMaintenance},
		{IP: "10.0.0.6", Err: errors.New("closed")},
	}
	var notes []string
	found, err := RecordScan(ctx, st, results, func(res talos.ScanResult, vipOf string) { notes = append(notes, res.IP+"/"+vipOf) })
	if err != nil || found != 1 {
		t.Fatalf("found=%d err=%v", found, err)
	}
	if len(notes) != 2 || notes[0] != "10.0.0.5/" || notes[1] != "10.0.0.250/lab" {
		t.Errorf("notes %v", notes)
	}
	m, err := st.GetNode(ctx, "10.0.0.5")
	if err != nil || m.MAC != inv.PrimaryMAC() || m.TalosVersion != "v1.14.1" || m.Source != "scan" {
		t.Errorf("row %+v %v", m, err)
	}
	if _, err := st.GetNode(ctx, "10.0.0.250"); err == nil {
		t.Error("a cluster VIP must not be recorded as a machine")
	}
	if n, err := RecordScan(ctx, st, results[2:], nil); err != nil || n != 0 {
		t.Errorf("nil note: %d %v", n, err)
	}
}
