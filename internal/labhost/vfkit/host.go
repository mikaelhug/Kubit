package vfkit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/labhost"
)

const (
	vfkitInstall = "brew install vfkit"
	vmnetInstall = "brew tap nirs/vmnet-helper && brew trust nirs/vmnet-helper && brew install nirs/vmnet-helper/vmnet-helper"
	minMacOS     = 26
)

func (h *Host) Capacity(ctx context.Context) (labhost.Capacity, error) {
	c := labhost.Capacity{Arch: runtime.GOARCH, CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	out, err := h.Run(ctx, "sysctl", "-n", "hw.ncpu", "hw.memsize", "hw.model")
	if err != nil {
		return c, err
	}
	f := strings.Fields(out)
	if len(f) < 3 {
		return c, fmt.Errorf("sysctl: unexpected output %q", out)
	}
	c.CPUs, _ = strconv.Atoi(f[0])
	mem, _ := strconv.ParseInt(f[1], 10, 64)
	c.MemMiB = int(mem >> 20)
	c.Model = f[2]
	if out, err := h.Run(ctx, "sysctl", "-n", "kern.hv_support"); err == nil {
		c.KVM = strings.TrimSpace(out) == "1"
	}
	c.ReserveMiB = max(4096, c.MemMiB/4)
	if out, err := h.Run(ctx, "scutil", "--get", "LocalHostName"); err == nil {
		c.Hostname = strings.TrimSpace(out)
	} else if hn, err := os.Hostname(); err == nil {
		c.Hostname = strings.TrimSuffix(hn, ".local")
	}
	if out, err := h.Run(ctx, "sw_vers", "-productVersion"); err == nil {
		c.OS = "macOS " + strings.TrimSpace(out)
	}
	if err := os.MkdirAll(h.Dir, 0o755); err != nil {
		return c, err
	}
	if out, err := h.Run(ctx, "df", "-k", h.Dir); err == nil {
		_, avail := parseDF(out)
		c.DiskGiB = int(avail >> 30)
	}
	if out, err := h.Run(ctx, h.VFKit, "--version"); err == nil {
		c.Hypervisor = strings.TrimSpace(strings.Replace(strings.TrimSpace(out), "vfkit version:", "vfkit", 1))
	}
	c.Problem, c.Command = h.problem(c)
	c.Ready = c.Problem == ""
	return c, nil
}

func (h *Host) problem(c labhost.Capacity) (string, string) {
	switch {
	case !c.KVM:
		return "This Mac has no hardware virtualisation.", ""
	case c.Hypervisor == "":
		return "vfkit is not installed.", vfkitInstall
	case !exists(h.VMNetRun):
		return "vmnet-helper is not installed.", vmnetInstall
	case c.OS != "" && macOSMajor(c.OS) < minMacOS:
		return fmt.Sprintf("VMs on this Mac need macOS %d or later.", minMacOS), ""
	case strings.Contains(h.Dir, ","):
		return "Kubit's home path contains a comma, which vfkit cannot take.", ""
	case len(h.sock("vm-00")) > maxSock:
		return "Kubit's home path is too long for the VMs' control sockets.", ""
	}
	return "", ""
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func macOSMajor(os string) int {
	v := strings.TrimPrefix(os, "macOS ")
	n, _ := strconv.Atoi(strings.SplitN(v, ".", 2)[0])
	return n
}

func (h *Host) HostMAC(ctx context.Context) (string, error) {
	if out, err := h.Run(ctx, "networksetup", "-getmacaddress", "en0"); err == nil {
		if m := firstMAC(out); m != "" {
			return m, nil
		}
	}
	out, err := h.Run(ctx, "networksetup", "-listallhardwareports")
	if err != nil {
		return "", err
	}
	if m := firstMAC(out); m != "" {
		return m, nil
	}
	return "", errors.New("this Mac reports no hardware address")
}

var macPattern = regexp.MustCompile(`(?i)Ethernet Address: ([0-9a-f]{1,2}(:[0-9a-f]{1,2}){5})`)

func firstMAC(out string) string {
	for _, m := range macPattern.FindAllStringSubmatch(out, -1) {
		if n := normalMAC(m[1]); n != "" && n != "00:00:00:00:00:00" {
			return n
		}
	}
	return ""
}

func (h *Host) Metrics(ctx context.Context) (labhost.Metrics, error) {
	now := time.Now()
	m := labhost.Metrics{At: now.UTC().Format(time.RFC3339)}
	out, err := h.Run(ctx, "sysctl", "-n", "vm.loadavg", "kern.boottime", "hw.memsize")
	if err != nil {
		return m, err
	}
	m.Load1, m.UptimeSec, m.MemTotal = parseSysctl(out, now)
	if out, err := h.Run(ctx, "vm_stat"); err == nil {
		m.MemUsed = parseVMStat(out)
	}
	if out, err := h.Run(ctx, "top", "-l", "2", "-n", "0", "-s", "1"); err == nil {
		m.CPUPct = parseTopCPU(out)
	}
	if out, err := h.Run(ctx, "df", "-k", h.Dir); err == nil {
		var avail int64
		m.DiskTotal, avail = parseDF(out)
		m.DiskUsed = m.DiskTotal - avail
	}
	if jobs, err := h.jobs(ctx); err == nil {
		for _, j := range jobs {
			if j.PID > 0 {
				m.VMsRunning++
			}
		}
	}
	return m, nil
}

var boottime = regexp.MustCompile(`sec = (\d+)`)

func parseSysctl(out string, now time.Time) (load1 float64, uptime, mem int64) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 3 {
		return
	}
	if f := strings.Fields(strings.Trim(lines[0], "{} ")); len(f) > 0 {
		load1, _ = strconv.ParseFloat(f[0], 64)
	}
	if m := boottime.FindStringSubmatch(lines[1]); m != nil {
		sec, _ := strconv.ParseInt(m[1], 10, 64)
		uptime = now.Unix() - sec
	}
	mem, _ = strconv.ParseInt(strings.TrimSpace(lines[2]), 10, 64)
	return
}

var pageSize = regexp.MustCompile(`page size of (\d+) bytes`)

func parseVMStat(out string) int64 {
	ps := int64(4096)
	if m := pageSize.FindStringSubmatch(out); m != nil {
		ps, _ = strconv.ParseInt(m[1], 10, 64)
	}
	var pages int64
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "Pages active", "Pages wired down", "Pages occupied by compressor":
			n, _ := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(v), "."), 10, 64)
			pages += n
		}
	}
	return pages * ps
}

var topIdle = regexp.MustCompile(`CPU usage:.*?([\d.]+)% idle`)

func parseTopCPU(out string) float64 {
	m := topIdle.FindAllStringSubmatch(out, -1)
	if len(m) == 0 {
		return 0
	}
	idle, _ := strconv.ParseFloat(m[len(m)-1][1], 64)
	return 100 - idle
}

func parseDF(out string) (total, avail int64) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		return 0, 0
	}
	f := strings.Fields(lines[len(lines)-1])
	if len(f) < 4 {
		return 0, 0
	}
	t, _ := strconv.ParseInt(f[1], 10, 64)
	a, _ := strconv.ParseInt(f[3], 10, 64)
	return t << 10, a << 10
}
