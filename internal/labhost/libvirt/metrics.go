package libvirt

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/labhost"
)

const metricsScript = `read l1 rest < /proc/loadavg; echo "load1=$l1"
echo "cpu1=$(head -1 /proc/stat)"; sleep 2; echo "cpu2=$(head -1 /proc/stat)"
awk '/MemTotal/{t=$2}/MemAvailable/{a=$2}END{print "memtotal=" t*1024; print "memavail=" a*1024}' /proc/meminfo
df -B1 --output=used,size ` + vmDir + ` 2>/dev/null | tail -1 | awk '{print "diskused=" $1; print "disktotal=" $2}'
echo "vms=$(virsh list --name 2>/dev/null | grep -c .)"
echo "uptime=$(cut -d. -f1 /proc/uptime)"`

func (c *Client) Metrics(ctx context.Context) (labhost.Metrics, error) {
	out, err := c.Run(ctx, metricsScript)
	if err != nil {
		return labhost.Metrics{}, err
	}
	return parseMetrics(out, time.Now()), nil
}

func parseMetrics(out string, at time.Time) labhost.Metrics {
	kv := keyValues(out)
	m := labhost.Metrics{At: at.UTC().Format(time.RFC3339)}
	m.Load1, _ = strconv.ParseFloat(kv["load1"], 64)
	m.MemTotal, _ = strconv.ParseInt(kv["memtotal"], 10, 64)
	avail, _ := strconv.ParseInt(kv["memavail"], 10, 64)
	if m.MemTotal > 0 {
		m.MemUsed = m.MemTotal - avail
	}
	m.DiskUsed, _ = strconv.ParseInt(kv["diskused"], 10, 64)
	m.DiskTotal, _ = strconv.ParseInt(kv["disktotal"], 10, 64)
	m.VMsRunning, _ = strconv.Atoi(kv["vms"])
	m.UptimeSec, _ = strconv.ParseInt(kv["uptime"], 10, 64)
	m.CPUPct = cpuPct(kv["cpu1"], kv["cpu2"])
	return m
}

func cpuPct(a, b string) float64 {
	total := func(line string) (busy, all int64) {
		f := strings.Fields(line)
		if len(f) < 5 || f[0] != "cpu" {
			return 0, 0
		}
		for i, s := range f[1:] {
			v, _ := strconv.ParseInt(s, 10, 64)
			all += v
			if i != 3 && i != 4 {
				busy += v
			}
		}
		return
	}
	b1, t1 := total(a)
	b2, t2 := total(b)
	if t2 <= t1 {
		return 0
	}
	return float64(b2-b1) * 100 / float64(t2-t1)
}

const updatesScript = `timeout 120 apt-get -qq update >/dev/null 2>&1 || true
inst=$(apt-get -s -o Debug::NoLocking=true full-upgrade 2>/dev/null | grep '^Inst ')
echo "updates=$(printf '%s\n' "$inst" | grep -c '^Inst ')"
echo "security=$(printf '%s\n' "$inst" | grep -ci 'security')"
echo "reboot=$([ -f /var/run/reboot-required ] && echo yes || echo no)"
echo "kernel=$(uname -r)"
echo "installed=$(ls -1 /boot/vmlinuz-* 2>/dev/null | sed 's#.*/vmlinuz-##' | sort -V | tail -1)"
echo "release=$(. /etc/os-release 2>/dev/null && echo "$PRETTY_NAME")"
echo "unattended=$(grep -qs 'Unattended-Upgrade "1"' /etc/apt/apt.conf.d/20auto-upgrades && echo yes || echo no)"`

func (c *Client) CheckUpdates(ctx context.Context) (labhost.Updates, error) {
	out, err := c.Run(ctx, updatesScript)
	if err != nil {
		return labhost.Updates{}, err
	}
	return parseUpdates(out, time.Now()), nil
}

func parseUpdates(out string, at time.Time) labhost.Updates {
	kv := keyValues(out)
	u := labhost.Updates{RebootRequired: kv["reboot"] == "yes", KernelRunning: kv["kernel"], KernelInstalled: kv["installed"], Release: kv["release"], Unattended: kv["unattended"] == "yes", CheckedAt: at.UTC().Format(time.RFC3339)}
	u.Count, _ = strconv.Atoi(kv["updates"])
	u.Security, _ = strconv.Atoi(kv["security"])
	return u
}

func (c *Client) Upgrade(ctx context.Context) (string, error) {
	return c.Run(ctx, `export DEBIAN_FRONTEND=noninteractive; apt-get -qq update && apt-get -y -q -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold full-upgrade && apt-get -y -q autoremove`)
}

func (c *Client) Reboot(ctx context.Context) error {
	_, err := c.Run(ctx, `nohup sh -c 'sleep 1; systemctl reboot' >/dev/null 2>&1 &`)
	return err
}

func (c *Client) ShutdownVMs(ctx context.Context, grace time.Duration) error {
	_, err := c.Run(ctx, fmt.Sprintf(`for d in $(virsh list --name); do [ -n "$d" ] && virsh shutdown $d >/dev/null 2>&1; done
end=$(( $(date +%%s) + %d ))
while [ $(date +%%s) -lt $end ] && [ -n "$(virsh list --name | grep .)" ]; do sleep 2; done
for d in $(virsh list --name); do [ -n "$d" ] && virsh destroy $d >/dev/null 2>&1; done; true`, int(grace.Seconds())))
	return err
}

func keyValues(out string) map[string]string {
	kv := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, "="); i > 0 {
			kv[line[:i]] = strings.TrimSpace(line[i+1:])
		}
	}
	return kv
}
