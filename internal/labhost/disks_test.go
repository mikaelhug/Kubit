package labhost

import "testing"

func TestPreferredLinkRanksStableIDs(t *testing.T) {
	for want, links := range map[string][]string{
		"wwn-0x600508b1001c4d5e":         {"scsi-SHP_LOGICAL_VOLUME_PDNLH0BRH8M0UL", "wwn-0x600508b1001c4d5e", "scsi-3600508b1001c4d5e"},
		"nvme-eui.0025388b91b1c2d3":      {"nvme-Samsung_SSD_980_S64DNX0R", "nvme-eui.0025388b91b1c2d3", "nvme-eui.0025388b91b1c2d3-part1"},
		"ata-Samsung_SSD_860_EVO_S3Z9NB": {"ata-Samsung_SSD_860_EVO_S3Z9NB-part2", "ata-Samsung_SSD_860_EVO_S3Z9NB"},
		"":                               {"usb-SanDisk_Cruzer_4C53-0:0", "wwn-0x5000c5 bad", "ata-x-part1"},
	} {
		if got := PreferredLink(links); got != want {
			t.Errorf("%v: %q, want %q", links, got, want)
		}
	}
}

func TestDiskKeyFallsBack(t *testing.T) {
	cases := []struct {
		links                 []string
		wwid, serial, devPath string
		want                  string
	}{
		{[]string{"scsi-3600508b1001c4d5e"}, "naa.600508b1001c4d5e", "PDNLH0", "/dev/sda", "scsi-3600508b1001c4d5e"},
		{nil, "t10.ATA     QEMU HARDDISK   QM00001", "", "/dev/sda", "wwid-t10.ATA_QEMU_HARDDISK_QM00001"},
		{nil, "", "S3Z9NB0K", "/dev/sdb", "serial-S3Z9NB0K"},
		{nil, "", "", "/dev/vdb", "/dev/vdb"},
	}
	for _, c := range cases {
		if got := DiskKey(c.links, c.wwid, c.serial, c.devPath); got != c.want {
			t.Errorf("%+v: %q", c, got)
		}
	}
}

func TestMatchDiskPrefersStableIdentity(t *testing.T) {
	host := []HostDisk{
		{DiskRef: DiskRef{Key: "wwn-0xa", DevPath: "/dev/sdb", Links: []string{"wwn-0xa", "scsi-3a"}, WWID: "naa.a", Serial: "A1", SizeBytes: 300 << 30}},
		{DiskRef: DiskRef{Key: "wwn-0xb", DevPath: "/dev/sda", Links: []string{"wwn-0xb"}, WWID: "naa.b", Serial: "B1", SizeBytes: 2000 << 30}},
		{DiskRef: DiskRef{Key: "/dev/vdc", DevPath: "/dev/vdc", SizeBytes: 100 << 30}},
		{DiskRef: DiskRef{Key: "/dev/vdd", DevPath: "/dev/vdd", Serial: "dup", SizeBytes: 100 << 30}},
		{DiskRef: DiskRef{Key: "/dev/vde", DevPath: "/dev/vde", Serial: "dup", SizeBytes: 100 << 30}},
	}
	cases := []struct {
		name string
		ref  DiskRef
		want string
	}{
		{"link survives a renamed device", DiskRef{Key: "scsi-3a", DevPath: "/dev/sda", Links: []string{"scsi-3a"}, SizeBytes: 300 << 30}, "/dev/sdb"},
		{"wwid", DiskRef{Key: "wwid-NAA.B", WWID: "NAA.B "}, "/dev/sda"},
		{"serial", DiskRef{Key: "serial-A1", Serial: "A1"}, "/dev/sdb"},
		{"device path with the same size", DiskRef{Key: "/dev/vdc", DevPath: "/dev/vdc", SizeBytes: 100 << 30}, "/dev/vdc"},
		{"device path with another size", DiskRef{Key: "x", DevPath: "/dev/vdc", SizeBytes: 50 << 30}, ""},
		{"ambiguous serial", DiskRef{Key: "serial-dup", Serial: "dup"}, ""},
	}
	for _, c := range cases {
		d, ok := MatchDisk(c.ref, host)
		if got := map[bool]string{true: d.DevPath}[ok]; got != c.want {
			t.Errorf("%s: matched %q, want %q", c.name, got, c.want)
		}
	}
}

func TestParseDiskChoice(t *testing.T) {
	for in, want := range map[string]struct {
		whole bool
		key   string
		bad   bool
	}{
		"": {}, "image:debian": {key: "debian"}, "whole:wwn-0xa": {whole: true, key: "wwn-0xa"}, "image:/dev/vdb": {key: "/dev/vdb"},
		"whole:": {bad: true}, "disk:x": {bad: true}, "x": {bad: true},
	} {
		whole, key, err := ParseDiskChoice(in)
		if (err != nil) != want.bad || whole != want.whole || key != want.key {
			t.Errorf("%q: %v %q %v", in, whole, key, err)
		}
	}
}

func TestSafeDeviceAndPoolNames(t *testing.T) {
	for path, ok := range map[string]bool{"/dev/disk/by-id/wwn-0x600508b1": true, "/dev/sdb": true, "/dev/nvme0n1": true, "/dev/disk/by-id/a b": false, "/dev/sdb;reboot": false, "/etc/passwd": false, "/dev/../etc": false} {
		if SafeDevice(path) != ok {
			t.Errorf("SafeDevice(%q) != %v", path, ok)
		}
	}
	if NextPoolName([]Pool{{Name: SystemPool}, {Name: "pool1"}, {Name: "pool3"}}) != "pool2" || !ValidPoolName("pool12") || ValidPoolName("pool") || ValidPoolName("../x") {
		t.Error("pool names")
	}
}
