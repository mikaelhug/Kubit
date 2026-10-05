package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"runtime"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/labhost"
	"github.com/mikael/kubit/internal/labhost/vfkit"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
)

func (s *Server) localDriver() (labhost.Driver, error) {
	if s.manager.Local == nil {
		return nil, errors.New("VMs on this machine are not supported here")
	}
	return s.manager.Local()
}

func (s *Server) localHost(ctx context.Context) *store.Machine {
	rows, err := s.store.ListNodes(ctx, "")
	if err != nil {
		return nil
	}
	for i := range rows {
		if rows[i].LabHost != nil && rows[i].LabHost.Driver == labhost.DriverVFKit {
			return &rows[i]
		}
	}
	return nil
}

func (s *Server) handleLabLocal(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"supported": false}
	d, err := s.localDriver()
	if err != nil {
		out["problem"] = err.Error()
		writeJSON(w, http.StatusOK, out)
		return
	}
	defer d.Close()
	capa, err := d.Capacity(labhost.FreshCapacity(r.Context()))
	if err != nil {
		writeErr(w, err)
		return
	}
	out["supported"], out["capacity"], out["problem"], out["command"], out["subnet"] = true, capa, capa.Problem, capa.Command, vfkit.Subnet
	if h := s.localHost(r.Context()); h != nil {
		out["host"] = h.MAC
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleLabLocalCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Driver string `json:"driver"`
		labPlan
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, badRequest(`body: {"driver":"vfkit","vms":{…},"cluster":{…}}`))
		return
	}
	plan := req.labPlan
	if req.Driver != labhost.DriverVFKit {
		writeErr(w, badRequest("driver must be vfkit"))
		return
	}
	if plan.Manual || plan.Network != "" || plan.Disk != "" || plan.VMs != nil && plan.VMs.usesDisks() {
		writeErr(w, badRequest("manual, network and disks do not apply to VMs on this Mac"))
		return
	}
	if code, err := s.checkLabPlan(r.Context(), &plan); err != nil {
		writeErr(w, &statusError{Status: code, Msg: err.Error()})
		return
	}
	if h := s.localHost(r.Context()); h != nil {
		writeErr(w, conflict("This Mac is already a lab host."))
		return
	}
	d, err := s.localDriver()
	if err != nil {
		writeErr(w, conflict(err.Error()))
		return
	}
	defer d.Close()
	capa, err := d.Capacity(labhost.FreshCapacity(r.Context()))
	if err != nil {
		writeErr(w, err)
		return
	}
	if capa.Problem != "" {
		writeErr(w, &statusError{Status: http.StatusConflict, Msg: capa.Problem, Code: "vfkit-missing", Command: capa.Command})
		return
	}
	if plan.VMs != nil {
		if need, free := plan.VMs.totalMem(), freeMiB(capa, nil); need > free {
			writeErr(w, &statusError{Status: http.StatusUnprocessableEntity, Msg: fmt.Sprintf("%d MiB requested, %d MiB free (this Mac keeps %s)", need, free, mib(capa.Reserve()))})
			return
		}
	}
	id, ok := d.(labhost.Identity)
	if !ok {
		writeErr(w, &statusError{Status: http.StatusInternalServerError, Msg: "this driver cannot name its host"})
		return
	}
	mac, err := id.HostMAC(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	if m, err := s.store.GetMachine(r.Context(), mac); err == nil && (m.Cluster != "" || m.LabHost != nil) {
		writeErr(w, conflict("This Mac's machine record is in use; retire it first."))
		return
	}
	hw := placeholderHardware(talos.Inventory{Manufacturer: "Apple", Product: capa.Model, CPUs: capa.CPUs, MemoryBytes: uint64(capa.MemMiB) << 20})
	if err := s.store.UpsertNode(r.Context(), store.NodeRow{MAC: mac, IP: vfkit.Gateway, Hostname: capa.Hostname, Source: "labhost", State: "labhost", Arch: capa.Arch, Hardware: hw}); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.store.SetLabHost(r.Context(), mac, &store.LabHost{State: "setup", Driver: labhost.DriverVFKit, Capacity: capa}); err != nil {
		writeErr(w, err)
		return
	}
	opID, err := s.runOperation("labhost:"+mac, "labhost.local", map[string]any{"mac": mac, "plan": plan}, func(ctx context.Context, sink cluster.Sink) (any, error) {
		return s.labLocalOp(ctx, sink, mac, plan)
	})
	if err != nil {
		if host, e := s.store.GetMachine(context.WithoutCancel(r.Context()), mac); e == nil {
			if _, rerr := s.releaseLabHost(context.WithoutCancel(r.Context()), host); rerr != nil {
				log.Printf("lab host %s: release: %v", mac, rerr)
			}
		}
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"operationId": opID, "mac": mac})
}

func (s *Server) labLocalOp(ctx context.Context, sink cluster.Sink, mac string, plan labPlan) (result any, err error) {
	defer func() {
		if err == nil {
			return
		}
		sink.Emit(cluster.Warn, "", "", "setup failed, removing the lab host: %v", err)
		rctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if host, e := s.store.GetMachine(rctx, mac); e == nil {
			if _, rerr := s.releaseLabHost(rctx, host); rerr != nil {
				sink.Emit(cluster.Warn, "", "", "release: %v", rerr)
			}
		}
	}()
	stop := keepAwake(ctx)
	defer stop()
	steps := cluster.Steps("setup", "Check vfkit and vmnet-helper, fetch the Talos ISO")
	if plan.VMs != nil {
		steps = append(steps, cluster.Steps("define", "Create the VMs", "vmboot", "Wait for Talos maintenance mode")...)
	}
	if plan.Cluster != nil {
		steps = append(steps, cluster.Steps("cluster", "Design and create the cluster")...)
	}
	sink.Plan(steps...)
	sink.Begin("setup")
	host, err := s.store.GetMachine(ctx, mac)
	if err != nil {
		return nil, err
	}
	lc, err := s.manager.LabDial(ctx, host)
	if err != nil {
		return nil, err
	}
	defer lc.Close()
	lh, err := s.labSetup(ctx, lc, host, sink)
	if err != nil {
		return nil, err
	}
	_ = s.store.Audit(ctx, "", "labhost.local", mac)
	sink.Emit(cluster.Done, "setup", "", "lab host ready: %d CPUs, %d MiB RAM (%s kept for macOS), %d GiB free for VMs", lh.Capacity.CPUs, lh.Capacity.MemMiB, mib(lh.Capacity.Reserve()), lh.Capacity.DiskGiB)
	sink.End("setup")
	if plan.VMs == nil {
		return lh, nil
	}
	return s.labRunPlan(ctx, mac, plan, sink)
}

func keepAwake(ctx context.Context) func() {
	if runtime.GOOS != "darwin" {
		return func() {}
	}
	cctx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(cctx, "caffeinate", "-i")
	if err := cmd.Start(); err != nil {
		cancel()
		return func() {}
	}
	return func() {
		cancel()
		_ = cmd.Wait()
	}
}

func (s *Server) labAutostart(ctx context.Context, host *store.Machine) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	d, err := s.manager.LabDial(ctx, host)
	if err != nil {
		return
	}
	defer d.Close()
	if a, ok := d.(labhost.Autostarter); ok {
		if err := a.Autostart(ctx); err != nil {
			log.Printf("lab host %s: autostart: %v", host.MAC, err)
		}
	}
}
