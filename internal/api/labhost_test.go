package api

import (
	"bytes"
	"encoding/json"
	"strings"
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
		hw, _ := json.Marshal(talos.Inventory{CPUs: 2, MemoryBytes: 3 << 30, Arch: "amd64", Disks: []talos.Disk{{DevPath: "/dev/vda", SizeBytes: 20 << 30}, {DevPath: "/dev/vdb", SizeBytes: 40 << 30}}})
		if err := st.UpsertNode(ctx, store.NodeRow{IP: "10.0.0.1" + string(rune('0'+i)), MAC: mac, Arch: "amd64", State: "maintenance", Source: "lab", Hardware: hw}); err != nil {
			t.Fatal(err)
		}
		_ = st.SetMachineHost(ctx, mac, "aa:aa:aa:aa:aa:01")
		macs = append(macs, mac)
	}
	one, err := s.labDesign(ctx, "lab", macs, map[string]bool{macs[0]: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(one.ControlPlanes()) != 1 || len(one.Workers()) != 3 || one.ControlPlanes()[0].Hostname != "lab-cp-01" || one.Workers()[2].Hostname != "lab-worker-03" {
		t.Errorf("1-cp design wrong: %+v", one.Spec.Nodes)
	}
	if one.ControlPlanes()[0].MAC != macs[0] {
		t.Errorf("control plane must be the planned VM %s, got %s", macs[0], one.ControlPlanes()[0].MAC)
	}
	for _, n := range one.Spec.Nodes {
		if n.InstallDisk.Path != "/dev/vda" || len(n.DataDisks) != 1 || n.DataDisks[0] != "/dev/vdb" {
			t.Errorf("%s: install %s data %v", n.Hostname, n.InstallDisk.Path, n.DataDisks)
		}
	}
	if one.Spec.ControlPlane.VIP != "" || one.Spec.ControlPlane.Endpoint == "" {
		t.Errorf("single control plane: no VIP, endpoint = the control plane; got vip=%q endpoint=%q", one.Spec.ControlPlane.VIP, one.Spec.ControlPlane.Endpoint)
	}
	three, err := s.labDesign(ctx, "lab", macs, map[string]bool{macs[0]: true, macs[1]: true, macs[2]: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(three.ControlPlanes()) != 3 || len(three.Workers()) != 1 || three.Spec.ControlPlane.VIP == "" {
		t.Errorf("3-cp design wrong: cps=%d workers=%d vip=%q", len(three.ControlPlanes()), len(three.Workers()), three.Spec.ControlPlane.VIP)
	}
	planned, err := labCluster{Name: "lab", ControlPlanes: 1, EphemeralSize: "25GiB"}.apply(one)
	if err != nil || planned.Spec.Storage.EphemeralSize != "25GiB" {
		t.Errorf("the plan's /var size reaches cluster.yaml: %v %q", err, planned.Spec.Storage.EphemeralSize)
	}
}

func TestLabClusterCarriesTheVarSize(t *testing.T) {
	s, _, _ := localServer(t)
	rec := call(t, s, "POST", "/api/v1/machines/aa:bb:cc:dd:ee:ff/labhost", `{"manual":true,"vms":{"count":1,"cpus":2,"memMiB":3072,"diskGiB":60},"cluster":{"name":"lab","controlPlanes":1,"ephemeralSize":"5GiB"}}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "at least 10GiB") {
		t.Errorf("a /var under 10 GiB is refused before the install: %d %s", rec.Code, rec.Body)
	}
}

func TestLabDesignControlPlaneIsThePlannedVM(t *testing.T) {
	c, _ := store.NewCrypto(bytes.Repeat([]byte{9}, 32))
	dir := t.TempDir()
	st, err := store.Open(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := New("test", cluster.NewManager(st, dir), "", c)
	ctx := t.Context()
	sizes := []uint64{2 << 30, 1 << 30, 1 << 30}
	var macs []string
	for i, mem := range sizes {
		mac := "52:54:00:6b:02:0" + string(rune('1'+i))
		hw, _ := json.Marshal(talos.Inventory{CPUs: 2, MemoryBytes: mem, Arch: "amd64", Disks: []talos.Disk{{DevPath: "/dev/vda", SizeBytes: 20 << 30}}})
		if err := st.UpsertNode(ctx, store.NodeRow{IP: "10.0.1.1" + string(rune('1'+i)), MAC: mac, Arch: "amd64", State: "maintenance", Source: "lab", Hardware: hw}); err != nil {
			t.Fatal(err)
		}
		_ = st.SetMachineHost(ctx, mac, "aa:aa:aa:aa:aa:02")
		macs = append(macs, mac)
	}
	cl, err := s.labDesign(ctx, "lab", macs, map[string]bool{macs[0]: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(cl.ControlPlanes()) != 1 || cl.ControlPlanes()[0].MAC != macs[0] {
		t.Fatalf("control plane must be the 2 GiB VM %s, got %+v", macs[0], cl.ControlPlanes())
	}
	if cl.Spec.ControlPlane.Endpoint != "https://10.0.1.11:6443" {
		t.Errorf("endpoint must point at the control plane, got %q", cl.Spec.ControlPlane.Endpoint)
	}
}
