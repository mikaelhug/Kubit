package libvirt

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
)

const (
	debianSuite  = "trixie"
	debianMirror = "http://deb.debian.org/debian"
	User         = "kubit"
	vmDir        = "/var/lib/kubit/vms"
	bootDir      = "/var/lib/kubit/boot"
)

func NetbootURL(arch, file string) string {
	return fmt.Sprintf("%s/dists/%s/main/installer-%s/current/images/netboot/debian-installer/%s/%s", debianMirror, debianSuite, arch, arch, file)
}

type PreseedParams struct {
	Hostname  string
	Disk      string
	PublicKey string
	PostURL   string
	Timezone  string
	Mirror    string
	Arch      string
}

func (p PreseedParams) ProgressURL() string {
	return strings.TrimSuffix(p.PostURL, "/postinstall") + "/progress"
}

func packages(arch string) string {
	common := "qemu-utils libvirt-daemon-system libvirt-clients bridge-utils sudo curl ca-certificates unattended-upgrades"
	if arch == "arm64" {
		return "qemu-system-arm qemu-efi-aarch64 " + common
	}
	return "qemu-system-x86 ovmf " + common
}

var preseedTmpl = template.Must(template.New("preseed").Parse(`d-i debian-installer/locale string en_US.UTF-8
d-i keyboard-configuration/xkb-keymap select us
d-i netcfg/choose_interface select auto
d-i netcfg/get_hostname string {{.Hostname}}
d-i netcfg/get_domain string lab
d-i netcfg/hostname string {{.Hostname}}
d-i mirror/country string manual
d-i mirror/http/hostname string {{.MirrorHost}}
d-i mirror/http/directory string {{.MirrorPath}}
d-i mirror/http/proxy string
d-i passwd/root-login boolean false
d-i passwd/user-fullname string Kubit
d-i passwd/username string {{.User}}
d-i passwd/user-password-crypted password !
d-i user-setup/allow-password-weak boolean true
d-i clock-setup/utc boolean true
d-i time/zone string {{.Timezone}}
d-i clock-setup/ntp boolean true
d-i preseed/early_command string wget -q -O /dev/null "{{.ProgressURL}}?stage=installer" || true
{{if .Disk}}d-i partman-auto/disk string {{.Disk}}
d-i partman/early_command string \
  wget -q -O /dev/null "{{.ProgressURL}}?stage=partitioning" || true
{{else}}d-i partman/early_command string \
  DISK=$(list-devices disk | while read d; do echo "$(blockdev --getsize64 $d) $d"; done | sort -n | tail -1 | cut -d' ' -f2); \
  debconf-set partman-auto/disk "$DISK"; \
  wget -q -O /dev/null "{{.ProgressURL}}?stage=partitioning" || true
{{end}}d-i partman-auto/method string lvm
d-i partman-lvm/device_remove_lvm boolean true
d-i partman-md/device_remove_md boolean true
d-i partman-lvm/confirm boolean true
d-i partman-lvm/confirm_nooverwrite boolean true
d-i partman-auto-lvm/guided_size string max
d-i partman-auto/choose_recipe select atomic
d-i partman-partitioning/confirm_write_new_label boolean true
d-i partman/choose_partition select finish
d-i partman/confirm boolean true
d-i partman/confirm_nooverwrite boolean true
d-i partman-efi/non_efi_system boolean true
tasksel tasksel/first multiselect ssh-server
d-i pkgsel/include string {{.Packages}}
d-i pkgsel/upgrade select none
popularity-contest popularity-contest/participate boolean false
d-i grub-installer/only_debian boolean true
d-i grub-installer/with_other_os boolean false
d-i grub-installer/bootdev string default
d-i grub-installer/force-efi-extra-removable boolean true
d-i finish-install/reboot_in_progress note
d-i preseed/late_command string \
  wget -q -O /dev/null "{{.ProgressURL}}?stage=packages" || true; \
  in-target sh -c 'mkdir -p /home/{{.User}}/.ssh && echo "{{.PublicKey}}" > /home/{{.User}}/.ssh/authorized_keys && chown -R {{.User}}:{{.User}} /home/{{.User}}/.ssh && chmod 700 /home/{{.User}}/.ssh && chmod 600 /home/{{.User}}/.ssh/authorized_keys'; \
  in-target sh -c 'echo "{{.User}} ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/kubit && chmod 440 /etc/sudoers.d/kubit'; \
  in-target sh -c 'usermod -aG libvirt,kvm {{.User}}'; \
  in-target sh -c 'IF=$(ls /sys/class/net | grep -v -e lo -e virbr -e br | head -1); printf "auto lo\niface lo inet loopback\n\nauto $IF\niface $IF inet manual\n\nauto br0\niface br0 inet dhcp\n  bridge_ports $IF\n  bridge_stp off\n  bridge_fd 0\n" > /etc/network/interfaces'; \
  in-target sh -c 'mkdir -p {{.VMDir}} {{.BootDir}} && chown -R {{.User}}:{{.User}} /var/lib/kubit'; \
  in-target systemctl enable libvirtd ssh; \
  in-target sh -c 'curl -fsS "{{.PostURL}}" -o /root/kubit-postinstall.sh && sh /root/kubit-postinstall.sh || true'; \
  wget -q -O /dev/null "{{.ProgressURL}}?stage=late-done" || true
`))

func Preseed(p PreseedParams) (string, error) {
	mirror := p.Mirror
	if mirror == "" {
		mirror = debianMirror
	}
	host, path := splitMirror(mirror)
	tz := p.Timezone
	if tz == "" {
		tz = "UTC"
	}
	data := struct {
		PreseedParams
		MirrorHost, MirrorPath, User, VMDir, BootDir, Timezone, Packages string
	}{p, host, path, User, vmDir, bootDir, tz, packages(p.Arch)}
	var b bytes.Buffer
	if err := preseedTmpl.Execute(&b, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

func splitMirror(u string) (string, string) {
	u = strings.TrimPrefix(strings.TrimPrefix(u, "http://"), "https://")
	if i := strings.Index(u, "/"); i >= 0 {
		return u[:i], u[i:]
	}
	return u, "/debian"
}

func KernelArgs(preseedURL, hostname string) string {
	h := ""
	if hostname != "" {
		h = " netcfg/get_hostname=" + hostname + " netcfg/get_domain=lab"
	}
	return fmt.Sprintf("auto=true priority=critical url=%s interface=auto%s DEBIAN_FRONTEND=text console=tty0 ---", preseedURL, h)
}

func PostInstall(progressURL string) string {
	return `#!/bin/sh
set -e
virsh net-autostart --disable default 2>/dev/null || true
virsh net-destroy default 2>/dev/null || true
printf 'APT::Periodic::Update-Package-Lists "1";\nAPT::Periodic::Unattended-Upgrade "1";\n' > /etc/apt/apt.conf.d/20auto-upgrades
cat > /etc/systemd/system/kubit-booted.service <<'UNIT'
[Unit]
Description=Tell Kubit the lab host booted
After=network-online.target
Wants=network-online.target
ConditionPathExists=!/var/lib/kubit/BOOTED
[Service]
Type=oneshot
ExecStart=/bin/sh -c 'for i in $(seq 1 30); do curl -fsS -o /dev/null "` + progressURL + `?stage=booted" && break; sleep 5; done; touch /var/lib/kubit/BOOTED'
[Install]
WantedBy=multi-user.target
UNIT
systemctl enable kubit-booted.service
echo "kubit lab host ready" > /var/lib/kubit/READY
`
}
