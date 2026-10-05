package libvirt

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikael/kubit/internal/labhost"
)

func TestPreseedAndKernelArgs(t *testing.T) {
	out, err := Preseed(PreseedParams{Hostname: "lab-abc", Disk: labhost.DiskRef{DevPath: "/dev/nvme0n1", Links: []string{"nvme-eui.0025388b"}, WWID: "eui.0025388b", SizeBytes: 512 << 30}, PublicKey: "ssh-ed25519 AAAA kubit", PostURL: "http://10.0.0.2:8069/labhost/aa/postinstall"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"d-i netcfg/get_hostname string lab-abc", "P='/dev/nvme0n1'", "W='eui.0025388b'", "for l in 'nvme-eui.0025388b'", `debconf-set grub-installer/bootdev "$D"`, "d-i partman-auto/cap-ram string 1024", "ssh-ed25519 AAAA kubit", "bridge_ports $IF", "ip -o route show default", "libvirt-daemon-system", "mirror/http/hostname string deb.debian.org", "curl -fsS \"http://10.0.0.2:8069/labhost/aa/postinstall\""} {
		if !strings.Contains(out, want) {
			t.Errorf("preseed missing %q", want)
		}
	}
	auto, _ := Preseed(PreseedParams{Hostname: "x", PublicKey: "k", PostURL: "u"})
	if !strings.Contains(auto, "partman/early_command") || strings.Contains(auto, "partman-auto/disk string") || !strings.Contains(auto, "K=''") || !strings.Contains(auto, "/removable") {
		t.Error("without a disk the early command must pick one")
	}
	if strings.Count(out, "\nd-i partman/early_command string ") != 1 {
		t.Error("the early command is one preseed line")
	}
	args := KernelArgs("http://10.0.0.2:8069/labhost/aa/preseed", "lab-abc")
	if !strings.Contains(args, "auto=true") || !strings.Contains(args, "url=http://10.0.0.2:8069/labhost/aa/preseed") {
		t.Errorf("kernel args: %s", args)
	}
}

func TestDomainXMLAndMAC(t *testing.T) {
	xml, err := DomainXML(labhost.VMSpec{Name: "lab-vm-01", MAC: labhost.MAC(1, 1), CPUs: 2, MemMiB: 3072, DiskGiB: 20, Kernel: "/k", Initrd: "/i", Arch: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<kernel>/k</kernel>", "talos.platform=metal", "<memory unit='MiB'>3072</memory>", "<vcpu>2</vcpu>", "mac address='52:54:00:6b:01:01'", "source bridge='br0'", "/var/lib/kubit/vms/lab-vm-01.qcow2", "machine='q35'"} {
		if !strings.Contains(xml, want) {
			t.Errorf("domain xml missing %q", want)
		}
	}
	if !strings.Contains(xml, "<domain type='kvm'>") || !strings.Contains(xml, "/usr/share/OVMF/OVMF_CODE_4M.fd") || !strings.Contains(xml, "<cpu mode='host-passthrough'/>") {
		t.Error("amd64 domain must be KVM with OVMF and host CPU")
	}
	if !strings.Contains(xml, "console=ttyS0") {
		t.Error("amd64 serial console must be ttyS0")
	}
	armk, _ := DomainXML(labhost.VMSpec{Name: "a", MAC: labhost.MAC(1, 5), CPUs: 1, MemMiB: 1024, DiskGiB: 10, Kernel: "/k", Initrd: "/i", Arch: "arm64"})
	if !strings.Contains(armk, "console=ttyAMA0") || strings.Contains(armk, "console=ttyS0") {
		t.Errorf("arm64 serial console must be ttyAMA0, not ttyS0")
	}
	if labhost.SerialConsole("arm64") != "console=ttyAMA0" || labhost.SerialConsole("amd64") != "console=ttyS0" {
		t.Error("SerialConsole arch mapping wrong")
	}
	disk, _ := DomainXML(labhost.VMSpec{Name: "v", MAC: labhost.MAC(1, 2), CPUs: 1, MemMiB: 1024, DiskGiB: 10, Arch: "arm64"})
	if !strings.Contains(disk, "<boot dev='hd'/>") || strings.Contains(disk, "<kernel>") || !strings.Contains(disk, "aarch64") {
		t.Error("disk-boot arm64 domain wrong")
	}
	if !strings.Contains(disk, "/usr/share/AAVMF/AAVMF_CODE.fd") || !strings.Contains(disk, "<nvram template='/usr/share/AAVMF/AAVMF_VARS.fd'>/var/lib/kubit/vms/v.nvram</nvram>") {
		t.Error("arm64 domain must carry AAVMF")
	}
	tcg, _ := DomainXML(labhost.VMSpec{Name: "t", MAC: labhost.MAC(1, 4), CPUs: 1, MemMiB: 1024, DiskGiB: 10, Arch: "arm64", TCG: true})
	if !strings.Contains(tcg, "<domain type='qemu'>") || !strings.Contains(tcg, "<cpu mode='maximum'/>") {
		t.Error("TCG domain must not ask for KVM or the host CPU")
	}
	if strings.Contains(xml, "vdb") {
		t.Error("no data disk unless asked")
	}
	withData, _ := DomainXML(labhost.VMSpec{Name: "d", MAC: labhost.MAC(1, 3), CPUs: 1, MemMiB: 1024, DiskGiB: 10, DataGiB: 40, Arch: "amd64"})
	if !strings.Contains(withData, "/var/lib/kubit/vms/d-data.qcow2") || !strings.Contains(withData, "<target dev='vdb' bus='virtio'/>") {
		t.Error("data disk must be the second virtio disk")
	}
	if !kernelBlock.MatchString(xml) {
		t.Error("kernel block must be recognisable for SetDiskBoot")
	}
	if labhost.MAC(300, 5) != "52:54:00:6b:2c:05" {
		t.Errorf("MAC wrap: %s", labhost.MAC(300, 5))
	}
	priv, pub, err := GenerateKey()
	if err != nil || !strings.HasPrefix(pub, "ssh-ed25519 ") || !strings.Contains(string(priv), "OPENSSH PRIVATE KEY") {
		t.Errorf("key generation: %v %q", err, pub)
	}
}

func TestTalosKernelArgsAreShared(t *testing.T) {
	want := "talos.platform=metal console=ttyS0 console=tty0 init_on_alloc=1 slab_nomerge pti=on"
	if got := vmCmdline("amd64"); got != want {
		t.Errorf("amd64 cmdline %q", got)
	}
	if got := vmCmdline("arm64"); !strings.HasPrefix(got, "talos.platform=metal console=ttyAMA0 console=tty0 ") {
		t.Errorf("arm64 cmdline %q", got)
	}
	xml, err := DomainXML(labhost.VMSpec{Name: "v", MAC: labhost.MAC(1, 9), CPUs: 1, MemMiB: 1024, DiskGiB: 10, Kernel: "/k", Initrd: "/i", Arch: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(xml, "<cmdline>"+want+"</cmdline>") {
		t.Errorf("domain XML must carry the shared cmdline:\n%s", xml)
	}
	pxe := strings.Join(labhost.TalosKernelArgs("console=tty0", "console=ttyS0"), " ")
	if pxe != "talos.platform=metal console=tty0 console=ttyS0 init_on_alloc=1 slab_nomerge pti=on" {
		t.Errorf("pxe args %q", pxe)
	}
}

func TestPartmanResolvesTheInstallDisk(t *testing.T) {
	type disk struct {
		name, wwid string
		size       int64
		removable  bool
		usb        bool
		links      []string
	}
	host := []disk{
		{name: "sda", wwid: "naa.600508b1001ca", size: 300 << 30, links: []string{"wwn-0x600508b1001ca", "scsi-3600508b1001ca"}},
		{name: "sdb", wwid: "naa.600508b1001cb", size: 2000 << 30, links: []string{"wwn-0x600508b1001cb"}},
		{name: "sdc", size: 4000 << 30, usb: true},
		{name: "sdd", size: 3000 << 30, removable: true},
	}
	cases := []struct {
		name string
		ref  labhost.DiskRef
		want string
	}{
		{"by-id link", labhost.DiskRef{Links: []string{"scsi-3600508b1001ca"}, DevPath: "/dev/sdb", SizeBytes: 2000 << 30}, "sda"},
		{"wwid when the link is missing", labhost.DiskRef{Links: []string{"wwn-0xgone"}, WWID: "naa.600508b1001cb"}, "sdb"},
		{"device path with a matching size", labhost.DiskRef{DevPath: "/dev/sdb", SizeBytes: 2000 << 30}, "sdb"},
		{"device path with another size", labhost.DiskRef{DevPath: "/dev/sdb", SizeBytes: 999 << 30}, "kubit-no-disk"},
		{"no pin takes the largest fixed disk", labhost.DiskRef{}, "sdb"},
	}
	for _, c := range cases {
		dir, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range host {
			dev := dir + "/dev/" + d.name
			sys := dir + "/devices/pci/" + d.name
			if d.usb {
				sys = dir + "/devices/pci/usb1/" + d.name
			}
			for _, p := range []string{dir + "/dev", sys, dir + "/sys/block", dir + "/by-id"} {
				if err := os.MkdirAll(p, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			removable := map[bool]string{true: "1", false: "0"}[d.removable]
			for file, body := range map[string]string{dev: "", dev + ".size": fmt.Sprint(d.size), sys + "/removable": removable, sys + "/wwid": d.wwid} {
				if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(sys, dir+"/sys/block/"+d.name); err != nil {
				t.Fatal(err)
			}
			for _, l := range d.links {
				if err := os.Symlink(dev, dir+"/by-id/"+l); err != nil {
					t.Fatal(err)
				}
			}
		}
		bin := dir + "/bin"
		_ = os.MkdirAll(bin, 0o755)
		var devs []string
		for _, d := range host {
			devs = append(devs, dir+"/dev/"+d.name)
		}
		for name, body := range map[string]string{
			"list-devices": "printf '%s\\n' " + strings.Join(devs, " "),
			"blockdev":     `cat "$2.size"`,
			"debconf-set":  `echo "$1 $2" >> "$LOG"`,
			"wget":         `echo "wget $4" >> "$LOG"`,
		} {
			if err := os.WriteFile(bin+"/"+name, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		ref := c.ref
		if ref.DevPath != "" {
			ref.DevPath = "/dev/" + strings.TrimPrefix(ref.DevPath, "/dev/")
		}
		script := partmanEarly(PreseedParams{Disk: ref, PostURL: "http://k/postinstall"})
		script = strings.NewReplacer("/sys/block/", dir+"/sys/block/", "/dev/disk/by-id/", dir+"/by-id/", "P='/dev/", "P='"+dir+"/dev/", `[ -b "$P" ]`, `[ -e "$P" ]`, "D=/dev/kubit-no-disk", "D="+dir+"/dev/kubit-no-disk").Replace(script)
		if out, err := exec.Command("sh", "-n", "-c", script).CombinedOutput(); err != nil {
			t.Fatalf("%s: syntax: %v %s", c.name, err, out)
		}
		cmd := exec.Command("sh", "-c", script)
		cmd.Env = append(os.Environ(), "PATH="+bin+":/usr/bin:/bin", "LOG="+dir+"/log")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v %s", c.name, err, out)
		}
		log, _ := os.ReadFile(dir + "/log")
		want := dir + "/dev/" + c.want
		if !strings.Contains(string(log), "partman-auto/disk "+want+"\n") || !strings.Contains(string(log), "grub-installer/bootdev "+want+"\n") {
			t.Errorf("%s: want %s for partman and grub:\n%s", c.name, c.want, log)
		}
		if nodisk := strings.Contains(string(log), "stage=nodisk"); nodisk != (c.want == "kubit-no-disk") {
			t.Errorf("%s: nodisk reported %v", c.name, nodisk)
		}
	}
}
