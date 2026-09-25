package vfkit

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/netip"
	"os"
	"strconv"
	"strings"

	"github.com/mikael/kubit/internal/labhost"
)

type job struct {
	PID    int
	Status int
}

func (h *Host) jobs(ctx context.Context) (map[string]job, error) {
	out, err := h.Run(ctx, "launchctl", "list")
	if err != nil {
		return nil, err
	}
	return parseLaunchctlList(out, h.prefix()), nil
}

func parseLaunchctlList(out, prefix string) map[string]job {
	jobs := map[string]job{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 3 || !strings.HasPrefix(f[2], prefix) {
			continue
		}
		j := job{}
		j.PID, _ = strconv.Atoi(f[0])
		j.Status, _ = strconv.Atoi(f[1])
		jobs[f[2]] = j
	}
	return jobs
}

func (h *Host) List(ctx context.Context) ([]labhost.VM, error) {
	specs, err := h.specs()
	if err != nil {
		return nil, err
	}
	jobs, err := h.jobs(ctx)
	if err != nil {
		return nil, err
	}
	leases := map[string]string{}
	if f, err := os.Open(h.Leases); err == nil {
		leases = parseLeases(f)
		f.Close()
	}
	vms := []labhost.VM{}
	for _, sp := range specs {
		vm := labhost.VM{Name: sp.Name, MAC: sp.MAC, State: "shut off", CPUs: sp.CPUs, MemMiB: sp.MemMiB, DiskGiB: sp.DiskGiB, DataGiB: sp.DataGiB, Boot: sp.Boot, IP: leases[sp.MAC]}
		if j, ok := jobs[h.label(sp.Name)]; ok {
			switch {
			case j.PID > 0:
				vm.State = "running"
			case j.Status != 0:
				vm.State = "crashed"
			}
		}
		vms = append(vms, vm)
	}
	return vms, nil
}

func parseLeases(r io.Reader) map[string]string {
	subnet := netip.MustParsePrefix(Subnet)
	type lease struct {
		ip string
		at uint64
	}
	best := map[string]lease{}
	var ip, mac string
	var at uint64
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "{":
			ip, mac, at = "", "", 0
		case strings.HasPrefix(line, "ip_address="):
			ip = strings.TrimPrefix(line, "ip_address=")
		case strings.HasPrefix(line, "hw_address=1,"):
			mac = normalMAC(strings.TrimPrefix(line, "hw_address=1,"))
		case strings.HasPrefix(line, "lease="):
			at, _ = strconv.ParseUint(strings.TrimPrefix(strings.TrimPrefix(line, "lease="), "0x"), 16, 64)
		case line == "}":
			a, err := netip.ParseAddr(ip)
			if mac == "" || err != nil || !subnet.Contains(a) {
				continue
			}
			if cur, ok := best[mac]; !ok || at >= cur.at {
				best[mac] = lease{ip, at}
			}
		}
	}
	out := map[string]string{}
	for m, l := range best {
		out[m] = l.ip
	}
	return out
}

func normalMAC(s string) string {
	parts := strings.Split(strings.ToLower(s), ":")
	if len(parts) != 6 {
		return ""
	}
	for i, p := range parts {
		n, err := strconv.ParseUint(p, 16, 8)
		if err != nil {
			return ""
		}
		parts[i] = fmt.Sprintf("%02x", n)
	}
	return strings.Join(parts, ":")
}
