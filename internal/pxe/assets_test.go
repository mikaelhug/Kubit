package pxe

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestTidyKeepsTheNewestVersionsAndSweepsParts(t *testing.T) {
	dir := t.TempDir()
	c := NewCache(dir)
	schematic := "376567988ad370138ad8b2698212367b8edcb69b5fd68c80be1f2ec7d603b4ba"
	var names []string
	for _, v := range []string{"v1.9.5", "v1.10.0", "v1.10.0-beta.1", "v1.14.0", "v1.13.2"} {
		for _, f := range []string{"kernel-amd64", "initramfs-amd64.xz"} {
			names = append(names, "factory.talos.dev_image_"+schematic+"_"+v+"_"+f)
		}
	}
	names = append(names, "factory.talos.dev_image_abc_v1.1.0_kernel-arm64", "boot.ipxe.org_undionly.kpxe", "deb.debian.org_debian_dists_trixie_main_installer-amd64_current_images_netboot_debian-installer_amd64_linux")
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * time.Hour)
	part := filepath.Join(dir, ".boot.ipxe.org_ipxe.efi.123.part")
	os.WriteFile(part, []byte("x"), 0o644)
	os.Chtimes(part, old, old)
	if err := c.Tidy(3, "v1.9.5", time.Hour); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	var left []string
	for _, e := range entries {
		left = append(left, e.Name())
	}
	sort.Strings(left)
	want := []string{
		"boot.ipxe.org_undionly.kpxe",
		"deb.debian.org_debian_dists_trixie_main_installer-amd64_current_images_netboot_debian-installer_amd64_linux",
		"factory.talos.dev_image_" + schematic + "_v1.10.0_initramfs-amd64.xz",
		"factory.talos.dev_image_" + schematic + "_v1.10.0_kernel-amd64",
		"factory.talos.dev_image_" + schematic + "_v1.13.2_initramfs-amd64.xz",
		"factory.talos.dev_image_" + schematic + "_v1.13.2_kernel-amd64",
		"factory.talos.dev_image_" + schematic + "_v1.14.0_initramfs-amd64.xz",
		"factory.talos.dev_image_" + schematic + "_v1.14.0_kernel-amd64",
		"factory.talos.dev_image_" + schematic + "_v1.9.5_initramfs-amd64.xz",
		"factory.talos.dev_image_" + schematic + "_v1.9.5_kernel-amd64",
		"factory.talos.dev_image_abc_v1.1.0_kernel-arm64",
	}
	if fmt.Sprint(left) != fmt.Sprint(want) {
		t.Errorf("left after tidy:\n%s\nwant:\n%s", left, want)
	}
	if err := NewCache(filepath.Join(dir, "missing")).Tidy(3, "", time.Hour); err != nil {
		t.Errorf("a missing cache is not an error: %v", err)
	}
}

func TestCompareVersions(t *testing.T) {
	for _, c := range [][2]string{{"v1.10.0", "v1.9.9"}, {"v1.14.0", "v1.14.0-beta.1"}, {"v2.0.0", "v1.99.99"}, {"v1.14.1", "v1.14.0"}} {
		if compareVersions(c[0], c[1]) <= 0 || compareVersions(c[1], c[0]) >= 0 {
			t.Errorf("%s must sort after %s", c[0], c[1])
		}
	}
	if compareVersions("v1.14.0", "v1.14.0") != 0 {
		t.Error("equal versions")
	}
}
