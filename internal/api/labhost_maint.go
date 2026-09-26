package api

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/cluster"
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

func (s *Server) handleLabCheck(w http.ResponseWriter, r *http.Request) {
	host, ok := s.labHostOf(w, r)
	if !ok {
		return
	}
	if host.LabHost.Driver != "" {
		http.Error(w, "Not available on this lab host.", http.StatusConflict)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	lc, err := s.manager.LabSSH(ctx, host)
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

func (s *Server) labClusters(ctx context.Context, host *store.Machine) []string {
	var out []string
	for _, v := range host.LabHost.VMs {
		if vm, err := s.store.GetMachine(ctx, v.MAC); err == nil && vm.Cluster != "" && !slices.Contains(out, vm.Cluster) {
			out = append(out, vm.Cluster)
		}
	}
	return out
}

func (s *Server) handleLabMaintain(upgrade bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host, ok := s.labHostOf(w, r)
		if !ok {
			return
		}
		if host.LabHost.Driver != "" {
			http.Error(w, "Not available on this lab host.", http.StatusConflict)
			return
		}
		if host.LabHost.State != "ready" {
			http.Error(w, "the host is "+host.LabHost.State, http.StatusConflict)
			return
		}
		affected := s.labClusters(r.Context(), host)
		if r.URL.Query().Get("ignoreWindow") != "true" {
			for _, name := range affected {
				if msg, closed := s.windowClosed(r.Context(), name); closed {
					writeJSON(w, http.StatusConflict, map[string]string{"error": name + ": " + msg})
					return
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
		locks := append([]string{"labhost:" + host.MAC}, affected...)
		id, err := s.runOperationLocking(owner, locks, kind, map[string]any{"host": host.MAC, "clusters": affected}, func(ctx context.Context, sink cluster.Sink) (any, error) {
			return nil, s.labMaintain(ctx, host, upgrade, affected, sink)
		})
		if err == nil {
			_ = s.store.Audit(r.Context(), owner, kind, host.MAC)
		}
		accepted(w, id, err)
	}
}

const (
	labVMGrace    = 90 * time.Second
	labRebootWait = 10 * time.Minute
	labReadyWait  = 10 * time.Minute
)

func (s *Server) labMaintain(ctx context.Context, host *store.Machine, upgrade bool, affected []string, sink cluster.Sink) error {
	mac := host.MAC
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

	lc, err := s.manager.LabSSH(ctx, host)
	if err != nil {
		return err
	}
	defer lc.Close()
	logf := func(step string, level cluster.Level, format string, a ...any) {
		sink.Emit(level, step, host.IP, format, a...)
	}

	sink.Begin("check")
	u, err := lc.CheckUpdates(ctx)
	if err != nil {
		return fmt.Errorf("check: %w", err)
	}
	host.LabHost.Updates = &u
	logf("check", cluster.Info, "%s: %d package updates pending%s", u.Release, u.Count, map[bool]string{true: ", reboot already required", false: ""}[u.NeedsReboot()])
	sink.End("check")

	if upgrade {
		sink.Begin("upgrade")
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
		sink.End("upgrade")
	}

	if upgrade && !u.NeedsReboot() {
		logf("reboot", cluster.Done, "no reboot needed; VMs untouched")
		sink.Skip("reboot")
		updates := host.LabHost.Updates
		_ = s.store.UpdateLabHost(ctx, mac, func(lh *store.LabHost) { lh.Updates = updates })
		return nil
	}

	sink.Begin("vms")
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
	sink.End("vms")

	sink.Begin("reboot")
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
	sink.End("reboot")

	sink.Begin("resume")
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
	sink.End("resume")

	if len(affected) == 0 {
		return nil
	}
	sink.Begin("cluster")
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
	sink.End("cluster")
	return nil
}

func (s *Server) labWaitSSH(ctx context.Context, host *store.Machine, timeout time.Duration) (*labhost.Client, error) {
	deadline := time.Now().Add(timeout)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(15 * time.Second):
	}
	for time.Now().Before(deadline) {
		if lc := sshReady(ctx, host.IP, func() (*labhost.Client, error) { return s.manager.LabSSH(ctx, host) }); lc != nil {
			return lc, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	return nil, fmt.Errorf("the host did not come back with SSH within %s; check it from the machine page (AMT power)", timeout)
}
