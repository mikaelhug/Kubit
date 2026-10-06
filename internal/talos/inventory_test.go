package talos

import "testing"

func TestByIDLinksKeepsOnlyStableNames(t *testing.T) {
	got := byIDLinks([]string{"/dev/disk/by-path/pci-0000:03:00.0-scsi-0:1:0:0", "/dev/disk/by-id/wwn-0x600508b1", "/dev/disk/by-id/scsi-3600508b1", "/dev/disk/by-diskseq/1"})
	if len(got) != 2 || got[0] != "scsi-3600508b1" || got[1] != "wwn-0x600508b1" {
		t.Errorf("by-id links: %v", got)
	}
}
