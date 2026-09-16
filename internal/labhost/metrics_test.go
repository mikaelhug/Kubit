package labhost

import (
	"strings"
	"testing"
	"time"
)

const metricsFixture = `load1=1.42
cpu1=cpu  1000 0 500 8000 200 0 50 0 0 0
cpu2=cpu  1300 0 600 8500 250 0 60 0 0 0
memtotal=16000000000
memavail=4000000000
diskused=180000000000
disktotal=200000000000
vms=3
uptime=86400
`

func TestParseMetrics(t *testing.T) {
	at := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	m := parseMetrics(metricsFixture, at)
	if m.Load1 != 1.42 || m.MemTotal != 16e9 || m.MemUsed != 12e9 || m.DiskUsed != 180e9 || m.DiskTotal != 200e9 || m.VMsRunning != 3 || m.UptimeSec != 86400 || m.At != "2026-09-15T12:00:00Z" {
		t.Errorf("parsed %+v", m)
	}
	// Between the two readings 960 jiffies passed, 410 of them busy.
	if m.CPUPct < 42.6 || m.CPUPct > 42.8 {
		t.Errorf("cpu %.2f%%, want 42.7", m.CPUPct)
	}
	if got := parseMetrics("", at); got.CPUPct != 0 || got.MemUsed != 0 {
		t.Errorf("empty output must parse to zeros, got %+v", got)
	}
}

func TestParseUpdatesAndNeedsReboot(t *testing.T) {
	u := parseUpdates("updates=7\nsecurity=2\nreboot=no\nkernel=6.12.0-1-amd64\ninstalled=6.12.0-2-amd64\nrelease=Debian GNU/Linux 13 (trixie)\nunattended=yes\n", time.Now())
	if u.Count != 7 || u.Security != 2 || u.RebootRequired || !u.Unattended || u.Release != "Debian GNU/Linux 13 (trixie)" {
		t.Errorf("parsed %+v", u)
	}
	if !u.NeedsReboot() {
		t.Error("a newer installed kernel needs a reboot")
	}
	u = parseUpdates("updates=0\nreboot=yes\nkernel=a\ninstalled=a\n", time.Now())
	if !u.NeedsReboot() {
		t.Error("reboot-required flag needs a reboot")
	}
	u = parseUpdates("updates=0\nreboot=no\nkernel=a\ninstalled=a\n", time.Now())
	if u.NeedsReboot() {
		t.Error("same kernel and no flag: no reboot")
	}
}

func TestPostInstallEnablesUnattendedUpgrades(t *testing.T) {
	post := PostInstall("http://10.0.0.2:8069/labhost/aa/progress")
	for _, want := range []string{`Unattended-Upgrade "1"`, "kubit-booted.service", "http://10.0.0.2:8069/labhost/aa/progress?stage=booted", "/var/lib/kubit/READY"} {
		if !strings.Contains(post, want) {
			t.Errorf("post-install missing %q", want)
		}
	}
	out, _ := Preseed(PreseedParams{Hostname: "x", PublicKey: "k", PostURL: "http://10.0.0.2:8069/labhost/aa/postinstall"})
	for _, want := range []string{"unattended-upgrades", "qemu-system-x86 ovmf", `stage=installer`, `stage=packages`, `stage=late-done`, "http://10.0.0.2:8069/labhost/aa/progress?stage="} {
		if !strings.Contains(out, want) {
			t.Errorf("amd64 preseed missing %q", want)
		}
	}
	arm, _ := Preseed(PreseedParams{Hostname: "x", PublicKey: "k", PostURL: "u", Arch: "arm64"})
	if !strings.Contains(arm, "qemu-system-arm qemu-efi-aarch64") || strings.Contains(arm, "qemu-system-x86") {
		t.Error("arm64 preseed must install the arm emulator and AAVMF")
	}
}
