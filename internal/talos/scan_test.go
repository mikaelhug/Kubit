package talos_test

import (
	"testing"

	"github.com/mikael/kubit/internal/talos"
)

func TestExpandTargets(t *testing.T) {
	addrs, err := talos.ExpandTargets([]string{"192.168.64.0/29", "10.0.0.7"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, a := range addrs {
		got = append(got, a.String())
	}
	want := []string{"192.168.64.1", "192.168.64.2", "192.168.64.3", "192.168.64.4", "192.168.64.5", "192.168.64.6", "10.0.0.7"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] got %s want %s", i, got[i], want[i])
		}
	}
	for _, bad := range []string{"192.168.64.0/31", "nope", "10.0.0.0/8/x"} {
		if _, err := talos.ExpandTargets([]string{bad}); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestInventoryHelpers(t *testing.T) {
	inv := &talos.Inventory{
		IP: "10.0.0.5",
		Disks: []talos.Disk{
			{DevPath: "/dev/sda", SizeBytes: 300e9, Transport: "usb", Readonly: true},
			{DevPath: "/dev/nvme0n1", SizeBytes: 500e9, Transport: "nvme"},
			{DevPath: "/dev/sdb", SizeBytes: 2000e9, Transport: "sata"},
			{DevPath: "/dev/sr0", SizeBytes: 1e9, CDROM: true},
		},
		Links: []talos.Link{
			{Name: "eth0", MAC: "aa:aa:aa:aa:aa:aa", Addresses: []string{"10.0.1.5/24"}},
			{Name: "eth1", MAC: "bb:bb:bb:bb:bb:bb", Addresses: []string{"10.0.0.5/24"}},
		},
	}
	if got := inv.PrimaryMAC(); got != "bb:bb:bb:bb:bb:bb" {
		t.Errorf("PrimaryMAC = %s; want the link holding the node IP", got)
	}
	c := inv.InstallCandidates()
	if len(c) != 2 || c[0].DevPath != "/dev/sdb" || c[1].DevPath != "/dev/nvme0n1" {
		t.Errorf("InstallCandidates = %+v; want sdb then nvme0n1, no usb/cdrom", c)
	}
}
