package watch

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/labhost"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
)

func (w *Watcher) labLoop(ctx context.Context) {
	w.every(ctx, w.ServiceInterval, w.labHostsTick)
}

func (w *Watcher) labHostsTick(ctx context.Context) {
	rows, err := w.Store.ListNodes(ctx, "")
	if err != nil {
		return
	}
	for i := range rows {
		host := rows[i]
		if host.LabHost == nil || host.LabHost.State != "ready" {
			continue
		}
		w.labTick(ctx, &host)
	}
}

func (w *Watcher) labTick(ctx context.Context, host *store.Machine) {
	tctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	key := store.LabHostKey(host.MAC)
	lc, err := w.Manager.LabDial(tctx, host)
	if err != nil {
		w.labFailed(tctx, host, err)
		return
	}
	defer lc.Close()
	vms, err := lc.List(tctx)
	if err != nil {
		w.labFailed(tctx, host, err)
		return
	}
	if host.LabHost.Failures >= labUnreachableAfter {
		w.emit(tctx, key, []store.EventRow{{Cluster: key, Severity: "info", Kind: "labhost.back", Message: labName(host) + ": reachable again"}})
	}
	w.mu.Lock()
	w.labNoNet[host.MAC] = false
	w.mu.Unlock()
	w.noteOnline()
	host.LabHost.Failures = 0
	capa, err := lc.Capacity(tctx)
	if err == nil {
		host.LabHost.Capacity = capa
	}
	host.LabHost.VMs = vms
	now := time.Now()
	if m, err := lc.Metrics(tctx); err == nil {
		host.LabHost.Metrics = &m
		sm := store.Sample{CPUMilli: int64(m.CPUPct * 10), CPUCap: 1000, MemBytes: m.MemUsed, MemCap: m.MemTotal, Pods: m.VMsRunning, Ready: true, Reachable: true, Disk: m.DiskUsed, DiskCap: m.DiskTotal}
		_ = w.Store.AddSamples(tctx, key, now, []store.Sample{sm})
		if w.OnHostSample != nil {
			sm.TS = now.UTC().Format(time.RFC3339)
			w.OnHostSample(host.MAC, sm)
		}
		w.emit(tctx, key, w.labResourceEvents(tctx, host, m))
	}
	if up, ok := lc.(labhost.Updater); ok && (host.LabHost.Updates == nil || staleBy(host.LabHost.Updates.CheckedAt, time.Hour)) {
		if u, err := up.CheckUpdates(tctx); err == nil {
			host.LabHost.Updates = &u
			if (u.Count > 0 || u.NeedsReboot()) && time.Since(w.Store.LastEventAt(tctx, key, "labhost.updates")) > 24*time.Hour {
				w.emit(tctx, key, []store.EventRow{{Cluster: key, Severity: "info", Kind: "labhost.updates", Message: labName(host) + ": " + updatesSummary(u)}})
			}
		}
	}
	_ = w.Store.UpdateLabHost(tctx, host.MAC, func(lh *store.LabHost) {
		lh.Failures = host.LabHost.Failures
		lh.Capacity = host.LabHost.Capacity
		lh.VMs = host.LabHost.VMs
		lh.Metrics = host.LabHost.Metrics
		lh.Updates = host.LabHost.Updates
	})
	for _, vm := range vms {
		row, err := w.Store.GetMachine(tctx, vm.MAC)
		if err != nil {
			continue
		}
		if vm.State != "running" && row.Cluster == "" && row.State != "off" {
			_ = w.Store.SetNodeState(tctx, row.IP, "off")
			continue
		}
		if vm.State == "running" && row.State == "off" {
			_ = w.Store.SetNodeState(tctx, row.IP, "booting")
			row.State = "booting"
		}
		if vm.State == "running" && vm.IP != "" && (row.State == "booting" || row.IP == "") {
			res := talos.Probe(tctx, vm.IP, 2*time.Second)
			if res.Err == nil {
				r := cluster.RowFromScan(res)
				r.Source = "lab"
				_ = w.Store.UpsertNode(tctx, r)
				_ = w.Store.SetMachineHost(tctx, vm.MAC, host.MAC)
			}
		}
	}
}

const labUnreachableAfter = 3

func (w *Watcher) labFailed(ctx context.Context, host *store.Machine, err error) {
	key := store.LabHostKey(host.MAC)
	if cluster.Classify(err) == cluster.ReachNoNetwork {
		if cluster.ControlProbe(ctx, 2*time.Second) == cluster.ReachNoNetwork {
			w.mu.Lock()
			first := !w.labNoNet[host.MAC]
			w.labNoNet[host.MAC] = true
			w.mu.Unlock()
			if first {
				log.Printf("lab host %s: %v (Kubit's host cannot reach the network; not counted)", host.MAC, err)
			}
			w.noteOffline(cluster.ShortNet(err))
			return
		}
	}
	w.mu.Lock()
	w.labNoNet[host.MAC] = false
	w.mu.Unlock()
	log.Printf("lab host %s: %v", host.MAC, err)
	var failures int
	_ = w.Store.UpdateLabHost(ctx, host.MAC, func(lh *store.LabHost) {
		lh.Failures++
		failures = lh.Failures
	})
	host.LabHost.Failures = failures
	if failures == labUnreachableAfter {
		what := "no SSH"
		if host.LabHost.Driver != "" {
			what = "not answering"
		}
		w.emit(ctx, key, []store.EventRow{{Cluster: key, Severity: "critical", Kind: "labhost.unreachable", Message: fmt.Sprintf("%s: %s for %d checks (%v)", labName(host), what, labUnreachableAfter, err)}})
	}
	_ = w.Store.AddSamples(ctx, key, time.Now(), []store.Sample{{Reachable: false}})
}

const (
	labDiskWarn     = 85
	labDiskCritical = 95
	labDiskOK       = 80
	labMemWarn      = 92
	labMemOK        = 85
	labMemSamples   = 3
)

func (w *Watcher) labResourceEvents(ctx context.Context, host *store.Machine, m labhost.Metrics) []store.EventRow {
	key, name := store.LabHostKey(host.MAC), labName(host)
	var out []store.EventRow
	if m.DiskTotal > 0 {
		pct := int(m.DiskUsed * 100 / m.DiskTotal)
		open := w.Store.OpenEventSeverity(ctx, key, "", "labhost.disk-low")
		msg := fmt.Sprintf("%s: VM disk %d%% full (%s of %s)", name, pct, cluster.HumanBytes(uint64(m.DiskUsed)), cluster.HumanBytes(uint64(m.DiskTotal)))
		switch {
		case pct >= labDiskCritical && open != "critical":
			_ = w.Store.ResolveEvents(ctx, key, "", "labhost.disk-low")
			out = append(out, store.EventRow{Cluster: key, Severity: "critical", Kind: "labhost.disk-low", Message: msg})
		case pct >= labDiskWarn && pct < labDiskCritical && open == "":
			out = append(out, store.EventRow{Cluster: key, Severity: "warn", Kind: "labhost.disk-low", Message: msg})
		case pct < labDiskOK && open != "":
			out = append(out, store.EventRow{Cluster: key, Severity: "info", Kind: "labhost.disk-ok", Message: fmt.Sprintf("%s: VM disk back to %d%%", name, pct)})
		}
	}
	if m.MemTotal > 0 {
		pct := int(m.MemUsed * 100 / m.MemTotal)
		w.mu.Lock()
		if pct >= labMemWarn {
			w.memHigh[host.MAC]++
		} else {
			w.memHigh[host.MAC] = 0
		}
		high := w.memHigh[host.MAC]
		w.mu.Unlock()
		open := w.Store.HasOpenEvent(ctx, key, "", "labhost.memory-pressure")
		switch {
		case high >= labMemSamples && !open:
			out = append(out, store.EventRow{Cluster: key, Severity: "warn", Kind: "labhost.memory-pressure", Message: fmt.Sprintf("%s: memory %d%% used for %d checks; VMs may be swapped or killed", name, pct, high)})
		case pct < labMemOK && open:
			out = append(out, store.EventRow{Cluster: key, Severity: "info", Kind: "labhost.memory-ok", Message: fmt.Sprintf("%s: memory back to %d%%", name, pct)})
		}
	}
	return out
}

func labName(host *store.Machine) string {
	if host.LabHost != nil && host.LabHost.Capacity.Hostname != "" {
		return host.LabHost.Capacity.Hostname
	}
	if host.Hostname != "" {
		return host.Hostname
	}
	return host.MAC
}

func updatesSummary(u labhost.Updates) string {
	var parts []string
	if u.Count > 0 {
		parts = append(parts, fmt.Sprintf("%d package updates pending", u.Count))
	}
	if u.NeedsReboot() {
		parts = append(parts, "reboot required")
	}
	return strings.Join(parts, ", ")
}

func staleBy(ts string, d time.Duration) bool {
	t, err := time.Parse(time.RFC3339, ts)
	return err != nil || time.Since(t) > d
}
