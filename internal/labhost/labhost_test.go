package labhost

import (
	"strings"
	"testing"
)

func TestPreseedAndKernelArgs(t *testing.T) {
	out, err := Preseed(PreseedParams{Hostname: "lab-abc", Disk: "/dev/nvme0n1", PublicKey: "ssh-ed25519 AAAA kubit", PostURL: "http://10.0.0.2:8069/labhost/aa/postinstall"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"d-i netcfg/get_hostname string lab-abc", "d-i partman-auto/disk string /dev/nvme0n1", "ssh-ed25519 AAAA kubit", "bridge_ports $IF", "libvirt-daemon-system", "mirror/http/hostname string deb.debian.org", "curl -fsS \"http://10.0.0.2:8069/labhost/aa/postinstall\""} {
		if !strings.Contains(out, want) {
			t.Errorf("preseed missing %q", want)
		}
	}
	auto, _ := Preseed(PreseedParams{Hostname: "x", PublicKey: "k", PostURL: "u"})
	if !strings.Contains(auto, "partman/early_command") || strings.Contains(auto, "partman-auto/disk string") {
		t.Error("without a disk the early command must pick one")
	}
	args := KernelArgs("http://10.0.0.2:8069/labhost/aa/preseed", "lab-abc")
	if !strings.Contains(args, "auto=true") || !strings.Contains(args, "url=http://10.0.0.2:8069/labhost/aa/preseed") {
		t.Errorf("kernel args: %s", args)
	}
}

func TestDomainXMLAndMAC(t *testing.T) {
	xml, err := DomainXML(VMSpec{Name: "lab-vm-01", MAC: MAC(1, 1), CPUs: 2, MemMiB: 3072, DiskGiB: 20, Kernel: "/k", Initrd: "/i", Arch: "amd64"})
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
	disk, _ := DomainXML(VMSpec{Name: "v", MAC: MAC(1, 2), CPUs: 1, MemMiB: 1024, DiskGiB: 10, Arch: "arm64"})
	if !strings.Contains(disk, "<boot dev='hd'/>") || strings.Contains(disk, "<kernel>") || !strings.Contains(disk, "aarch64") {
		t.Error("disk-boot arm64 domain wrong")
	}
	// Without a UEFI loader an arm64 virt machine has nothing to boot a disk with.
	if !strings.Contains(disk, "/usr/share/AAVMF/AAVMF_CODE.fd") || !strings.Contains(disk, "<nvram template='/usr/share/AAVMF/AAVMF_VARS.fd'>/var/lib/kubit/vms/v.nvram</nvram>") {
		t.Error("arm64 domain must carry AAVMF")
	}
	tcg, _ := DomainXML(VMSpec{Name: "t", MAC: MAC(1, 4), CPUs: 1, MemMiB: 1024, DiskGiB: 10, Arch: "arm64", TCG: true})
	if !strings.Contains(tcg, "<domain type='qemu'>") || !strings.Contains(tcg, "<cpu mode='maximum'/>") {
		t.Error("TCG domain must not ask for KVM or the host CPU")
	}
	if strings.Contains(xml, "vdb") {
		t.Error("no data disk unless asked")
	}
	withData, _ := DomainXML(VMSpec{Name: "d", MAC: MAC(1, 3), CPUs: 1, MemMiB: 1024, DiskGiB: 10, DataGiB: 40, Arch: "amd64"})
	if !strings.Contains(withData, "/var/lib/kubit/vms/d-data.qcow2") || !strings.Contains(withData, "<target dev='vdb' bus='virtio'/>") {
		t.Error("data disk must be the second virtio disk")
	}
	if !kernelBlock.MatchString(xml) {
		t.Error("kernel block must be recognisable for SetDiskBoot")
	}
	if MAC(300, 5) != "52:54:00:6b:2c:05" {
		t.Errorf("MAC wrap: %s", MAC(300, 5))
	}
	priv, pub, err := GenerateKey()
	if err != nil || !strings.HasPrefix(pub, "ssh-ed25519 ") || !strings.Contains(string(priv), "OPENSSH PRIVATE KEY") {
		t.Errorf("key generation: %v %q", err, pub)
	}
}
