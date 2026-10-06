package talos_test

import (
	"testing"

	"github.com/mikael/kubit/internal/talos"
)

func TestInstallTargetsTheScannedMACAndNeverARemovableDisk(t *testing.T) {
	inv := &talos.Inventory{
		IP: "10.0.0.5",
		Disks: []talos.Disk{
			{DevPath: "/dev/sda", SizeBytes: 300e9, Transport: "usb", Readonly: true},
			{DevPath: "/dev/nvme0n1", SizeBytes: 500e9, Transport: "nvme"},
			{DevPath: "/dev/sdb", SizeBytes: 2000e9, Transport: "sata"},
			{DevPath: "/dev/sr0", SizeBytes: 1e9, CDROM: true},
			{SizeBytes: 1 << 40},
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
	if len(c) != 2 {
		t.Errorf("install candidates %+v; want sdb and nvme0n1", c)
	}
	for _, d := range c {
		if d.DevPath != "/dev/sdb" && d.DevPath != "/dev/nvme0n1" {
			t.Errorf("install candidate %+v; never usb, read-only, cdrom or a disk without a device path", d)
		}
	}
}
