package talos

import (
	"encoding/json"
	"testing"
)

func TestInventoryCarriesTPMAndWatchdog(t *testing.T) {
	b, err := json.Marshal(Inventory{TPM: true})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["tpm"] != true || m["watchdog"] != false {
		t.Errorf("inventory JSON: %s", b)
	}
	var old Inventory
	if err := json.Unmarshal([]byte(`{"kvm":true}`), &old); err != nil || old.TPM || old.Watchdog {
		t.Errorf("a record from before detection reads as absent: %+v %v", old, err)
	}
}

func TestByIDLinksKeepsOnlyStableNames(t *testing.T) {
	got := byIDLinks([]string{"/dev/disk/by-path/pci-0000:03:00.0-scsi-0:1:0:0", "/dev/disk/by-id/wwn-0x600508b1", "/dev/disk/by-id/scsi-3600508b1", "/dev/disk/by-diskseq/1"})
	if len(got) != 2 || got[0] != "scsi-3600508b1" || got[1] != "wwn-0x600508b1" {
		t.Errorf("by-id links: %v", got)
	}
	inv := Inventory{Disks: []Disk{{SizeBytes: 1 << 40}, {DevPath: "/dev/sda", SizeBytes: 1 << 30}}}
	if c := inv.InstallCandidates(); len(c) != 1 || c[0].DevPath != "/dev/sda" {
		t.Errorf("a disk without a device path is no install candidate: %+v", c)
	}
}
