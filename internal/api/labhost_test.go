package api

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
)

func TestLabDesignTopology(t *testing.T) {
	c, _ := store.NewCrypto(bytes.Repeat([]byte{8}, 32))
	dir := t.TempDir()
	st, err := store.Open(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := New("test", cluster.NewManager(st, dir), "", c)
	ctx := t.Context()
	var macs []string
	for i := 1; i <= 4; i++ {
		mac := "52:54:00:6b:01:0" + string(rune('0'+i))
		// The data disk is larger than the boot disk on purpose: vda must still be
		// the install disk and vdb the data volume.
		hw, _ := json.Marshal(talos.Inventory{CPUs: 2, MemoryBytes: 3 << 30, Arch: "amd64", Disks: []talos.Disk{{DevPath: "/dev/vda", SizeBytes: 20 << 30}, {DevPath: "/dev/vdb", SizeBytes: 40 << 30}}})
		if err := st.UpsertNode(ctx, store.NodeRow{IP: "10.0.0.1" + string(rune('0'+i)), MAC: mac, Arch: "amd64", State: "maintenance", Source: "lab", Hardware: hw}); err != nil {
			t.Fatal(err)
		}
		_ = st.SetMachineHost(ctx, mac, "aa:aa:aa:aa:aa:01")
		macs = append(macs, mac)
	}
	one, err := s.labDesign(ctx, "lab", macs, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(one.ControlPlanes()) != 1 || len(one.Workers()) != 3 || one.ControlPlanes()[0].Hostname != "lab-cp-01" || one.Workers()[2].Hostname != "lab-worker-03" {
		t.Errorf("1-cp design wrong: %+v", one.Spec.Nodes)
	}
	for _, n := range one.Spec.Nodes {
		if n.InstallDisk.Path != "/dev/vda" || len(n.DataDisks) != 1 || n.DataDisks[0] != "/dev/vdb" {
			t.Errorf("%s: install %s data %v", n.Hostname, n.InstallDisk.Path, n.DataDisks)
		}
	}
	if one.Spec.ControlPlane.VIP != "" || one.Spec.ControlPlane.Endpoint == "" {
		t.Errorf("single control plane: no VIP, endpoint = the control plane; got vip=%q endpoint=%q", one.Spec.ControlPlane.VIP, one.Spec.ControlPlane.Endpoint)
	}
	three, err := s.labDesign(ctx, "lab", macs, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(three.ControlPlanes()) != 3 || len(three.Workers()) != 1 || three.Spec.ControlPlane.VIP == "" {
		t.Errorf("3-cp design wrong: cps=%d workers=%d vip=%q", len(three.ControlPlanes()), len(three.Workers()), three.Spec.ControlPlane.VIP)
	}
}
