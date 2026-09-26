package config

import (
	"net/netip"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/cel"
	"github.com/siderolabs/talos/pkg/machinery/cel/celenv"
	blockres "github.com/siderolabs/talos/pkg/machinery/resources/block"
)

func TestUserVolume(t *testing.T) {
	match := cel.MustExpression(cel.ParseBooleanExpression("system_disk", celenv.DiskLocator()))
	v := userVolume("scratch", blockres.VolumeTypePartition, match)
	if v.MetaName != "scratch" || *v.VolumeType != blockres.VolumeTypePartition || v.FilesystemSpec.FilesystemType != blockres.FilesystemTypeXFS || v.ProvisioningSpec.DiskSelectorSpec.Match.String() != "system_disk" {
		t.Errorf("volume: %s %v %v %q", v.MetaName, *v.VolumeType, v.FilesystemSpec.FilesystemType, v.ProvisioningSpec.DiskSelectorSpec.Match.String())
	}
	d, err := dataVolume(2, "/dev/vdc")
	if err != nil {
		t.Fatal(err)
	}
	if d.MetaName != "data-2" || *d.VolumeType != blockres.VolumeTypeDisk || d.ProvisioningSpec.DiskSelectorSpec.Match.String() != `disk.dev_path == "/dev/vdc"` {
		t.Errorf("data volume: %s %v %q", d.MetaName, *d.VolumeType, d.ProvisioningSpec.DiskSelectorSpec.Match.String())
	}
	s, err := systemVolume()
	if err != nil {
		t.Fatal(err)
	}
	if s.MetaName != SystemDataVolume || *s.VolumeType != blockres.VolumeTypePartition || s.ProvisioningSpec.ProvisioningGrow == nil || !*s.ProvisioningSpec.ProvisioningGrow || s.FilesystemSpec.FilesystemType != blockres.FilesystemTypeXFS {
		t.Errorf("system volume: %s %v", s.MetaName, *s.VolumeType)
	}
	if _, err := dataVolume(1, ""); err == nil {
		t.Error("a data volume needs a device path")
	}
}

func TestOrEmpty(t *testing.T) {
	var m map[string]string
	orEmpty(&m)["a"] = "b"
	if m["a"] != "b" {
		t.Fatalf("map %v", m)
	}
	orEmpty(&m)["c"] = "d"
	if len(m) != 2 {
		t.Errorf("an existing map is kept: %v", m)
	}
}

func TestDefaultMetalLBRangeAndInRange(t *testing.T) {
	if got := DefaultMetalLBRange("192.168.5.17"); got != "192.168.5.200-192.168.5.220" {
		t.Errorf("range %q", got)
	}
	if got := DefaultMetalLBRange("fd00::1"); got != "" {
		t.Errorf("IPv6 gets no default range, got %q", got)
	}
	lo, hi, _ := ParseIPRange("10.0.0.200-10.0.0.220")
	for ip, want := range map[string]bool{"10.0.0.199": false, "10.0.0.200": true, "10.0.0.210": true, "10.0.0.220": true, "10.0.0.221": false} {
		if got := inRange(netip.MustParseAddr(ip), lo, hi); got != want {
			t.Errorf("inRange(%s) = %v", ip, got)
		}
	}
}
