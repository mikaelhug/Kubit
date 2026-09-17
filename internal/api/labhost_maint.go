package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/labhost"
	"github.com/mikael/kubit/internal/store"
)

func (s *Server) labMaintRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/machines/{mac}/labhost/samples", s.handleLabSamples)
	r.HandleFunc("POST /api/v1/machines/{mac}/labhost/check", s.handleLabCheck)
	r.HandleFunc("POST /api/v1/machines/{mac}/labhost/update", s.handleLabMaintain(true))
	r.HandleFunc("POST /api/v1/machines/{mac}/labhost/reboot", s.handleLabMaintain(false))
}

func (s *Server) handleLabSamples(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.Samples(r.Context(), store.LabHostKey(r.PathValue("mac")), "", time.Now().Add(-sampleRange(r.URL.Query().Get("range"))))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// handleLabCheck refreshes the package index now instead of at the hourly tick.
func (s *Server) handleLabCheck(w http.ResponseWriter, r *http.Request) {
	host, err := s.store.GetMachine(r.Context(), strings.ToLower(r.PathValue("mac")))
	if err != nil || host.LabHost == nil {
		http.Error(w, "not a lab host", http.StatusNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	lc, err := s.manager.LabDial(ctx, host)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer lc.Close()
	u, err := lc.CheckUpdates(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	host.LabHost.Updates = &u
	_ = s.store.UpdateLabHost(ctx, host.MAC, func(lh *store.LabHost) { lh.Updates = &u })
	writeJSON(w, http.StatusOK, u)
}

// labClusters lists the clusters with members among this host's VMs: the ones a
// host reboot takes down, so the ones whose maintenance windows apply.
func (s *Server) labClusters(ctx context.Context, host *store.Machine) []string {
	var out []string
	for _, v := range host.LabHost.VMs {
		if vm, err := s.store.GetMachine(ctx, v.MAC); err == nil && vm.Cluster != "" && !contains(out, vm.Cluster) {
			out = append(out, vm.Cluster)
		}
	}
	return out
}

// handleLabMaintain runs Update host (upgrade, then reboot only if needed) or Reboot
// host. Either can take every VM down, so the affected clusters' maintenance windows
// gate it like any other disruptive operation.
func (s *Server) handleLabMaintain(upgrade bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host, err := s.store.GetMachine(r.Context(), strings.ToLower(r.PathValue("mac")))
		if err != nil || host.LabHost == nil {
			http.Error(w, "not a lab host", http.StatusNotFound)
			return
		}
		if host.LabHost.State != "ready" {
			http.Error(w, "the host is "+host.LabHost.State, http.StatusConflict)
			return
		}
		affected := s.labClusters(r.Context(), host)
		if r.URL.Query().Get("ignoreWindow") != "true" {
			for _, name := range affected {
				if c, _, err := s.manager.LoadCluster(r.Context(), name); err == nil {
					if open, next := c.Spec.Maintenance.Open(time.Now()); !open {
						writeJSON(w, http.StatusConflict, map[string]string{"error": windowMessage(name, c.Spec.Maintenance, next)})
						return
					}
				}
			}
		}
		kind, owner := "labhost.reboot", ""
		if upgrade {
			kind = "labhost.update"
		}
		if len(affected) > 0 {
			owner = affected[0]
		}
		id, err := s.runOperation(owner, kind, map[string]any{"host": host.MAC, "clusters": affected}, func(ctx contextT, sink clusterSink) (any, error) {
			return nil, s.labMaintain(ctx, host, upgrade, affected, sink)
		})
		if err != nil {
			writeErr(w, err)
			return
		}
		_ = s.store.Audit(r.Context(), owner, kind, host.MAC)
		writeJSON(w, http.StatusAccepted, map[string]any{"operationId": id})
	}
}

func windowMessage(cluster string, m config.Maintenance, next time.Time) string {
	msg := fmt.Sprintf("%s: outside the maintenance window (%s", cluster, m.Window)
	if m.Timezone != "" {
		msg += " " + m.Timezone
	}
	msg += ")"
	if !next.IsZero() {
		msg += "; next opens " + next.Format("Mon 2006-01-02 15:04 MST")
	}
	return msg + ". Add ?ignoreWindow=true to override."
}

const (
	labVMGrace    = 90 * time.Second
	labRebootWait = 10 * time.Minute
	labReadyWait  = 10 * time.Minute
)

func (s *Server) labMaintain(ctx context.Context, host *store.Machine, upgrade bool, affected []string, sink clusterSink) error {
	mac := host.MAC
	// The watcher's tick would count the reboot as failures; park it until done.
	// Merge only State so a concurrent watcher tick cannot clobber it, and use a
	// detached context so the "ready" restore still runs if the op was cancelled.
	setState := func(c context.Context, state string) {
		_ = s.store.UpdateLabHost(c, mac, func(lh *store.LabHost) { lh.State = state })
		if h, err := s.store.GetMachine(c, mac); err == nil && h.LabHost != nil {
			host = h
		}
	}
	setState(ctx, "updating")
	defer func() {
		rctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		setState(rctx, "ready")
	}()

	lc, err := s.manager.LabDial(ctx, host)
	if err != nil {
		return err
	}
	defer lc.Close()
	step := func(name string, status cluster.StepStatus) {
		sink(clusterEvent{Time: time.Now(), Kind: "step", Step: name, Status: status})
	}
	logf := func(step string, level cluster.Level, format string, a ...any) {
		sink(clusterEvent{Time: time.Now(), Kind: "log", Level: level, Step: step, Node: host.IP, Message: fmt.Sprintf(format, a...)})
	}

	step("check", cluster.StepRunning)
	u, err := lc.CheckUpdates(ctx)
	if err != nil {
		return fmt.Errorf("check: %w", err)
	}
	host.LabHost.Updates = &u
	logf("check", cluster.Info, "%s: %d package updates pending%s", u.Release, u.Count, map[bool]string{true: ", reboot already required", false: ""}[u.NeedsReboot()])
	step("check", cluster.StepDone)

	if upgrade {
		step("upgrade", cluster.StepRunning)
		if u.Count == 0 {
			logf("upgrade", cluster.Info, "nothing to upgrade")
		} else {
			out, err := lc.Upgrade(ctx)
			for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
				if line != "" {
					logf("upgrade", cluster.Info, "%s", line)
				}
			}
			if err != nil {
				return fmt.Errorf("upgrade: %w", err)
			}
			if u, err = lc.CheckUpdates(ctx); err == nil {
				host.LabHost.Updates = &u
			}
		}
		step("upgrade", cluster.StepDone)
	}

	if upgrade && !u.NeedsReboot() {
		logf("reboot", cluster.Done, "no reboot needed; VMs untouched")
		step("reboot", cluster.StepSkipped)
		updates := host.LabHost.Updates
		_ = s.store.UpdateLabHost(ctx, mac, func(lh *store.LabHost) { lh.Updates = updates })
		return nil
	}

	step("vms", cluster.StepRunning)
	running := 0
	for _, v := range host.LabHost.VMs {
		if v.State == "running" {
			running++
		}
	}
	logf("vms", cluster.Info, "stopping %d VMs (graceful, %s grace)", running, labVMGrace)
	if err := lc.ShutdownVMs(ctx, labVMGrace); err != nil {
		return fmt.Errorf("stop VMs: %w", err)
	}
	step("vms", cluster.StepDone)

	step("reboot", cluster.StepRunning)
	if err := lc.Reboot(ctx); err != nil {
		return fmt.Errorf("reboot: %w", err)
	}
	lc.Close()
	logf("reboot", cluster.Info, "rebooting; waiting for SSH")
	lc, err = s.labWaitSSH(ctx, host, labRebootWait)
	if err != nil {
		return err
	}
	defer lc.Close()
	if u, err := lc.CheckUpdates(ctx); err == nil {
		host.LabHost.Updates = &u
		logf("reboot", cluster.Done, "back on kernel %s", u.KernelRunning)
	}
	step("reboot", cluster.StepDone)

	step("resume", cluster.StepRunning)
	// Autostart brings VMs back; anything defined before autostart existed is
	// started here.
	for _, v := range host.LabHost.VMs {
		if v.State == "running" {
			_ = lc.Start(ctx, v.Name)
		}
	}
	vms, listErr := lc.List(ctx)
	m, metricsErr := lc.Metrics(ctx)
	_ = s.store.UpdateLabHost(ctx, mac, func(lh *store.LabHost) {
		if listErr == nil {
			lh.VMs = vms
		}
		if metricsErr == nil {
			lh.Metrics = &m
		}
	})
	nowRunning := 0
	if metricsErr == nil {
		nowRunning = m.VMsRunning
	} else if host.LabHost.Metrics != nil {
		nowRunning = host.LabHost.Metrics.VMsRunning
	}
	logf("resume", cluster.Done, "%d VMs running", nowRunning)
	step("resume", cluster.StepDone)

	if len(affected) == 0 {
		return nil
	}
	step("cluster", cluster.StepRunning)
	for _, name := range affected {
		c, _, err := s.manager.LoadCluster(ctx, name)
		if err != nil {
			return err
		}
		var names []string
		for _, n := range c.Spec.Nodes {
			if row, err := s.store.GetMachine(ctx, n.MAC); err == nil && row.Host == mac {
				names = append(names, n.Hostname)
			}
		}
		kc, err := s.manager.KubeClient(ctx, name)
		if err != nil {
			return err
		}
		if err := kc.WaitReady(ctx, names, labReadyWait, func(ready, total int) { logf("cluster", cluster.Info, "%s: %d/%d nodes Ready", name, ready, total) }); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		logf("cluster", cluster.Done, "%s: all %d nodes on this host Ready", name, len(names))
	}
	step("cluster", cluster.StepDone)
	return nil
}

// labWaitSSH polls port 22 and then a real login until the host answers again.
func (s *Server) labWaitSSH(ctx context.Context, host *store.Machine, timeout time.Duration) (*labhost.Client, error) {
	deadline := time.Now().Add(timeout)
	// Give the host time to actually go down so an early dial does not catch the
	// old session.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(15 * time.Second):
	}
	for time.Now().Before(deadline) {
		d := net.Dialer{Timeout: 2 * time.Second}
		if conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host.IP, "22")); err == nil {
			conn.Close()
			if lc, err := s.manager.LabDial(ctx, host); err == nil {
				if _, err := lc.Run(ctx, "test -f /var/lib/kubit/READY"); err == nil {
					return lc, nil
				}
				lc.Close()
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	return nil, fmt.Errorf("the host did not come back with SSH within %s; check it from the machine page (AMT power)", timeout)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
