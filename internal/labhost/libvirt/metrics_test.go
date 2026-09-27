package libvirt

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	m, last := parseMetrics(metricsFixture, "", at)
	if m.Load1 != 1.42 || m.MemTotal != 16e9 || m.MemUsed != 12e9 || m.DiskUsed != 180e9 || m.DiskTotal != 200e9 || m.VMsRunning != 3 || m.UptimeSec != 86400 || m.At != "2026-09-15T12:00:00Z" {
		t.Errorf("parsed %+v", m)
	}
	if m.CPUPct < 42.6 || m.CPUPct > 42.8 {
		t.Errorf("cpu %.2f%%, want 42.7", m.CPUPct)
	}
	if last != "cpu  1300 0 600 8500 250 0 60 0 0 0" {
		t.Errorf("the later sample must be kept for the next tick, got %q", last)
	}
	if got, _ := parseMetrics("", "", at); got.CPUPct != 0 || got.MemUsed != 0 {
		t.Errorf("empty output must parse to zeros, got %+v", got)
	}
	next, last := parseMetrics("cpu1=cpu  1600 0 700 9000 300 0 70 0 0 0\n", last, at)
	if next.CPUPct < 42.6 || next.CPUPct > 42.8 || last != "cpu  1600 0 700 9000 300 0 70 0 0 0" {
		t.Errorf("delta against the previous tick: %.2f%% %q", next.CPUPct, last)
	}
	if rebooted, _ := parseMetrics("cpu1=cpu  10 0 5 80 2 0 0 0 0 0\n", last, at); rebooted.CPUPct != 0 {
		t.Errorf("counters that went back (reboot) must not give a reading, got %.2f", rebooted.CPUPct)
	}
}

func TestMetricsScriptSamplesOnlyWithoutAPreviousReading(t *testing.T) {
	if s := metricsScript(true); !strings.Contains(s, "sleep 1") || !strings.Contains(s, "cpu2=") {
		t.Errorf("a first reading samples for a second:\n%s", s)
	}
	if s := metricsScript(false); strings.Contains(s, "sleep") || strings.Contains(s, "cpu2=") {
		t.Errorf("later readings must not sleep:\n%s", s)
	}
	for _, sample := range []bool{true, false} {
		if out, err := exec.Command("sh", "-n", "-c", metricsScript(sample)).CombinedOutput(); err != nil {
			t.Errorf("script does not parse: %v %s", err, out)
		}
	}
	now := time.Now()
	rememberCPU("h1", "cpu 1 2 3 4 5", now.Add(-cpuSampleMaxAge-time.Second))
	if previousCPU("h1", now) != "" {
		t.Error("a reading older than the max age must be sampled afresh")
	}
	rememberCPU("h1", "cpu 1 2 3 4 5", now)
	if previousCPU("h1", now) == "" || previousCPU("h2", now) != "" {
		t.Error("readings are kept per host")
	}
	rememberCPU("h1", "", now)
	if previousCPU("h1", now) != "" {
		t.Error("an empty reading clears the host")
	}
}

func TestUpgradeRunsDetachedAndWaitsForTheLock(t *testing.T) {
	for _, want := range []string{"(" + dpkgConfigure + ") && apt-get", "DPkg::Lock::Timeout=600", "full-upgrade", "autoremove", "upgrade.rc"} {
		if !strings.Contains(upgradeJob, want) {
			t.Errorf("upgrade job lacks %q", want)
		}
	}
	if strings.Count(upgradeJob, "DPkg::Lock::Timeout=600") != 3 {
		t.Error("every apt-get call must wait for the dpkg lock")
	}
	if !strings.Contains(upgradeStart, "systemd-run --quiet --collect --unit=kubit-upgrade.service") || !strings.Contains(upgradeStart, "systemctl is-active -q kubit-upgrade.service") {
		t.Errorf("the upgrade must run as its own unit, once:\n%s", upgradeStart)
	}
	for _, script := range []string{upgradeStart, upgradeWait, upgradeJob, upgradeIdle} {
		if out, err := exec.Command("sh", "-n", "-c", script).CombinedOutput(); err != nil {
			t.Errorf("script does not parse: %v %s\n%s", err, out, script)
		}
	}
}

func TestConfigureWaitsForTheDpkgLock(t *testing.T) {
	for _, c := range []struct {
		name  string
		codes string
		rc    int
		calls int
	}{
		{"locked twice", "2 2 0", 0, 3},
		{"broken package", "1", 1, 1},
		{"idle", "0", 0, 1},
		{"locked for good", "", 2, 121},
	} {
		dir := t.TempDir()
		dpkg := `n=$(cat "$D/calls" 2>/dev/null || echo 0); echo $((n+1)) > "$D/calls"; set -- $(cat "$D/codes"); shift $n 2>/dev/null || exit 2; exit ${1:-2}`
		for name, script := range map[string]string{"dpkg": dpkg, "sleep": "true"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		os.WriteFile(filepath.Join(dir, "codes"), []byte(c.codes), 0o644)
		cmd := exec.Command("sh", "-c", "("+dpkgConfigure+")")
		cmd.Env = []string{"PATH=" + dir + ":/usr/bin:/bin", "D=" + dir}
		err := cmd.Run()
		rc := 0
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			rc = exit.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
		if rc != c.rc || strings.TrimSpace(string(calls)) != strconv.Itoa(c.calls) {
			t.Errorf("%s: rc %d after %s calls, want rc %d after %d", c.name, rc, strings.TrimSpace(string(calls)), c.rc, c.calls)
		}
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
