package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/labhost"
	"github.com/mikael/kubit/internal/labhost/vfkit"
	"github.com/mikael/kubit/internal/netx"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
)

func (s *Server) handleLabAddVMs(w http.ResponseWriter, r *http.Request) {
	mac := pathMAC(r)
	var req addVMsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.validate() != nil {
		writeErr(w, badRequest(`body: {"count":4,"cpus":2,"memMiB":3072,"diskGiB":20}; at least 1 vCPU, 2048 MiB, 8 GiB`))
		return
	}
	host, err := s.store.GetMachine(r.Context(), mac)
	if err != nil {
		writeErr(w, err)
		return
	}
	if host.LabHost == nil || host.LabHost.State != "ready" {
		writeErr(w, conflict("the lab host is not ready"))
		return
	}
	lh := host.LabHost
	if need, free := req.totalMem(), freeMiB(lh.Capacity, lh.VMs); need > free {
		writeErr(w, &statusError{Status: http.StatusUnprocessableEntity, Msg: fmt.Sprintf("%d MiB requested, %d MiB free (host keeps %s)", need, free, mib(lh.Capacity.Reserve()))})
		return
	}
	if req.usesDisks() {
		if lh.Driver == labhost.DriverVFKit {
			writeErr(w, badRequest("disk choices need a Debian lab host"))
			return
		}
		if _, err := planStorage(req.sizes(), lh.Capacity, lh.VMs); err != nil {
			writeErr(w, badRequest(err.Error()))
			return
		}
	}
	s.startOp(w, "labhost:"+mac, "labhost.vms", req, func(ctx context.Context, sink cluster.Sink) (any, error) {
		return s.labAddVMs(ctx, host, req, sink)
	})
}

func (s *Server) labAddVMs(ctx context.Context, host *store.Machine, req addVMsRequest, sink cluster.Sink) ([]string, error) {
	mac := host.MAC
	if fresh, err := s.store.GetMachine(ctx, mac); err == nil && fresh.LabHost != nil {
		host = fresh
	}
	lh := host.LabHost
	if need, free := req.totalMem(), freeMiB(lh.Capacity, lh.VMs); need > free {
		return nil, fmt.Errorf("the VMs need %d MiB, but the host has %d MiB free for VMs (%d MiB total, %s kept for the host); the host is set up — add smaller or fewer VMs from its Lab host tab", need, free, lh.Capacity.MemMiB, mib(lh.Capacity.Reserve()))
	}
	lc, err := s.manager.LabDial(ctx, host)
	if err != nil {
		return nil, err
	}
	defer lc.Close()
	existing, _ := lc.List(ctx)
	sizes := req.sizes()
	if _, ok := lc.(labhost.Storage); !ok && req.usesDisks() {
		return nil, errors.New("disk choices need a Debian lab host")
	}
	layout, err := s.applyStorage(ctx, lc, mac, sizes, existing, func(format string, args ...any) { sink.Emit(cluster.Info, "define", "", format, args...) })
	if err != nil {
		return nil, err
	}
	used := map[string]bool{}
	for _, vm := range existing {
		used[strings.ToLower(vm.MAC)] = true
	}
	if rows, err := s.store.ListNodes(ctx, ""); err == nil {
		for _, row := range rows {
			if row.Host != "" && netx.MACKey(row.Host) == netx.MACKey(mac) {
				used[strings.ToLower(row.MAC)] = true
			}
		}
	}
	next := len(existing) + 1
	prefix := req.Prefix
	if prefix == "" {
		prefix = "vm"
	}
	var created, createdMACs []string
	for i, size := range sizes {
		name := size.Name
		if name == "" || nameTaken(existing, name) {
			name = fmt.Sprintf("%s-%02d", prefix, next)
			for nameTaken(existing, name) {
				next++
				name = fmt.Sprintf("%s-%02d", prefix, next)
			}
		}
		vmMAC, err := nextVMMAC(lh.Index, used)
		if err != nil {
			return nil, err
		}
		used[vmMAC] = true
		spec := labhost.VMSpec{Name: name, MAC: vmMAC, CPUs: size.CPUs, MemMiB: size.MemMiB, DiskGiB: size.DiskGiB, DataGiB: size.DataGiB, Kernel: lh.Kernel, Initrd: lh.Initrd, ISO: lh.ISO, Arch: lh.Capacity.Arch, Bridge: lh.Capacity.Bridge, Routed: lh.Network == "routed", TCG: !lh.Capacity.KVM}
		if i < len(layout.VMs) {
			spec.System, spec.Data = layout.VMs[i].System, layout.VMs[i].Data
		}
		if err := lc.Define(ctx, spec); err != nil {
			for i, n := range created {
				_ = lc.Delete(ctx, n)
				_ = s.store.DeleteMachine(ctx, createdMACs[i])
			}
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		product := "KVM VM on " + labHostname(host)
		if lh.Driver == labhost.DriverVFKit {
			product = "Apple VM on " + lh.Capacity.Hostname
		}
		hw := placeholderHardware(talos.Inventory{Manufacturer: "Kubit lab", Product: product, Virtual: true, CPUs: size.CPUs, MemoryBytes: uint64(size.MemMiB) << 20})
		_ = s.store.UpsertNode(ctx, store.NodeRow{MAC: spec.MAC, Hostname: name, Source: "lab", State: "booting", Arch: lh.Capacity.Arch, Hardware: hw})
		_ = s.store.SetMachineHost(ctx, spec.MAC, mac)
		existing = append(existing, labhost.VM{Name: name, MAC: spec.MAC})
		created = append(created, name)
		createdMACs = append(createdMACs, spec.MAC)
		sink.Emit(cluster.Info, "define", name, "defined and started: %s, %d vCPU, %d MiB, %s, %s", size.Role, size.CPUs, size.MemMiB, placementText(spec), spec.MAC)
		next++
	}
	if vms, err := lc.List(ctx); err == nil {
		lh.VMs = vms
	}
	_ = s.store.UpdateLabHost(ctx, mac, func(l *store.LabHost) { l.VMs = lh.VMs })
	if err := s.labWaitMaintenance(ctx, lc, lh, mac, created, sink); err != nil {
		return nil, err
	}
	_ = s.store.Audit(ctx, "", "labhost.vms", fmt.Sprintf("%s +%d", mac, len(created)))
	sink.Emit(cluster.Done, "vmboot", "", "%d VM(s) in maintenance mode, ready to be picked for a cluster", len(created))
	return createdMACs, nil
}

func nextVMMAC(index int, used map[string]bool) (string, error) {
	for n := 1; n <= 255; n++ {
		if m := labhost.MAC(index, n); !used[m] {
			return m, nil
		}
	}
	return "", errors.New("no free VM MAC left on this lab host")
}

func placementText(s labhost.VMSpec) string {
	where := func(p labhost.Placement, gib int) string {
		switch {
		case p.Device != "":
			return "whole " + p.Device
		case p.Pool != "" && p.Pool != labhost.SystemPool:
			return fmt.Sprintf("%d GiB on %s", gib, p.Pool)
		}
		return fmt.Sprintf("%d GiB", gib)
	}
	out := where(s.System, s.DiskGiB)
	if s.Data.Device != "" || s.DataGiB > 0 {
		out += " + data " + where(s.Data, s.DataGiB)
	}
	return out
}

func (s *Server) labWaitMaintenance(ctx context.Context, lc labhost.Driver, lh *store.LabHost, mac string, created []string, sink cluster.Sink) error {
	pending := map[string]bool{}
	for _, n := range created {
		pending[n] = true
	}
	var subnets []netip.Addr
	if lh.Driver == labhost.DriverVFKit {
		subnets, _ = talos.ExpandTargets([]string{vfkit.Subnet})
	} else if v, err := s.store.GetSettings(ctx); err == nil {
		subnets, _ = talos.ExpandTargets(v.DiscoverySubnets)
	}
	deadline := time.Now().Add(6 * time.Minute)
	for len(pending) > 0 && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
		}
		vms, err := lc.List(ctx)
		if err != nil {
			continue
		}
		byMAC := map[string]*labhost.VM{}
		var results []talos.ScanResult
		for i := range vms {
			byMAC[strings.ToLower(vms[i].MAC)] = &vms[i]
			if pending[vms[i].Name] && vms[i].IP != "" {
				results = append(results, talos.Probe(ctx, vms[i].IP, 2*time.Second))
			}
		}
		if len(results) < len(pending) && len(subnets) > 0 {
			results = append(results, talos.Scan(ctx, subnets, 64, 2*time.Second)...)
		}
		for _, res := range results {
			if res.Err != nil || res.State != talos.StateMaintenance || res.Inventory == nil {
				continue
			}
			vm := byMAC[strings.ToLower(res.Inventory.PrimaryMAC())]
			if vm == nil || !pending[vm.Name] {
				continue
			}
			vm.IP = res.IP
			row := cluster.RowFromScan(res)
			row.Source = "lab"
			_ = s.store.UpsertNode(ctx, row)
			_ = s.store.SetMachineHost(ctx, vm.MAC, mac)
			delete(pending, vm.Name)
			sink.Emit(cluster.Info, "vmboot", vm.Name, "Talos maintenance mode at %s", vm.IP)
		}
		lh.VMs = vms
		_ = s.store.UpdateLabHost(ctx, mac, func(l *store.LabHost) { l.VMs = vms })
	}
	if len(pending) > 0 {
		names := make([]string, 0, len(pending))
		for n := range pending {
			names = append(names, n)
		}
		sort.Strings(names)
		hint := "check the VM console on the host: virsh console <name>"
		if lh.Driver == labhost.DriverVFKit {
			hint = "check " + filepath.Join(s.manager.Home, "vms", "<name>", "vfkit.log")
		}
		return fmt.Errorf("%s did not reach Talos maintenance mode within 6 minutes (%s)", strings.Join(names, ", "), hint)
	}
	return nil
}

func (s *Server) refreshLabVMs(ctx context.Context, lc labhost.Driver, mac string) {
	if vms, err := lc.List(ctx); err == nil {
		_ = s.store.UpdateLabHost(ctx, mac, func(lh *store.LabHost) { lh.VMs = vms })
	}
}

func (s *Server) deleteLabVMs(ctx context.Context, vms []store.Machine) error {
	byHost := map[string][]store.Machine{}
	for _, vm := range vms {
		byHost[vm.Host] = append(byHost[vm.Host], vm)
	}
	var errs []error
	for hostMAC, members := range byHost {
		if err := s.deleteHostVMs(ctx, hostMAC, members); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Server) deleteHostVMs(ctx context.Context, hostMAC string, members []store.Machine) error {
	host, err := s.store.GetMachine(ctx, hostMAC)
	if err != nil || host.LabHost == nil {
		return fmt.Errorf("lab host %s is no longer known", hostMAC)
	}
	lctx, cancel := context.WithTimeout(ctx, lockWait)
	unlock, err := s.locks.lockAllContext(lctx, []string{"labhost:" + hostMAC})
	cancel()
	if err != nil {
		return fmt.Errorf("its VMs were kept: %s", strings.ToLower(labBusy))
	}
	defer unlock()
	dctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	lc, err := s.manager.LabDial(dctx, host)
	if err != nil {
		return fmt.Errorf("its VMs were kept: %w", err)
	}
	defer lc.Close()
	var errs []error
	for _, m := range members {
		i := slices.IndexFunc(host.LabHost.VMs, func(v labhost.VM) bool { return strings.EqualFold(v.MAC, m.MAC) })
		if i < 0 {
			continue
		}
		name := host.LabHost.VMs[i].Name
		if err := lc.Delete(dctx, name); err != nil {
			errs = append(errs, fmt.Errorf("delete %s: %w", name, err))
			continue
		}
		_ = s.store.DeleteMachine(ctx, m.MAC)
		_ = s.store.Audit(ctx, "", "labhost.vm.delete", hostMAC+" "+name)
	}
	s.refreshLabVMs(dctx, lc, hostMAC)
	return errors.Join(errs...)
}

func waitVMOff(ctx context.Context, lc labhost.Driver, name string, within time.Duration) error {
	deadline := time.Now().Add(within)
	for {
		if vms, err := lc.List(ctx); err == nil {
			off := true
			for _, v := range vms {
				if v.Name == name && v.State == "running" {
					off = false
				}
			}
			if off {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not shut down within %s; force-stop it", name, within)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

func nameTaken(vms []labhost.VM, name string) bool {
	return slices.ContainsFunc(vms, func(v labhost.VM) bool { return v.Name == name })
}

func (s *Server) vmRow(ctx context.Context, host *store.Machine, name string) *store.Machine {
	if host.LabHost != nil {
		for _, v := range host.LabHost.VMs {
			if v.Name == name {
				if m, err := s.store.GetMachine(ctx, v.MAC); err == nil {
					return m
				}
			}
		}
	}
	return nil
}

func (s *Server) handleLabVMAction(w http.ResponseWriter, r *http.Request) {
	name, action := r.PathValue("name"), r.PathValue("action")
	host, ok := s.labHostOf(w, r)
	if !ok {
		return
	}
	mac := host.MAC
	s.startOp(w, "labhost:"+mac, "labhost.vm."+action, map[string]string{"host": mac, "vm": name}, func(ctx context.Context, sink cluster.Sink) (any, error) {
		lc, err := s.manager.LabDial(ctx, host)
		if err != nil {
			return nil, err
		}
		defer lc.Close()
		switch action {
		case "start":
			err = lc.Start(ctx, name)
		case "stop":
			if err = lc.Stop(ctx, name, false); err == nil {
				err = waitVMOff(ctx, lc, name, 90*time.Second)
			}
		case "kill":
			err = lc.Stop(ctx, name, true)
		case "reprovision":
			if vm := s.vmRow(ctx, host, name); vm != nil && vm.Cluster != "" {
				return nil, fmt.Errorf("%s is a member of %s; remove it from the cluster first", name, vm.Cluster)
			}
			if err = lc.SetTalosBoot(ctx, name, labhost.Boot{Kernel: host.LabHost.Kernel, Initrd: host.LabHost.Initrd, ISO: host.LabHost.ISO}, host.LabHost.Capacity.Arch); err != nil {
				break
			}
			if err = lc.Stop(ctx, name, true); err != nil {
				break
			}
			if err = lc.Start(ctx, name); err != nil {
				break
			}
			if vm := s.vmRow(ctx, host, name); vm != nil && vm.IP != "" {
				_ = s.store.SetNodeState(ctx, vm.IP, "booting")
			}
		default:
			return nil, fmt.Errorf("unknown action %q", action)
		}
		if err != nil {
			return nil, err
		}
		s.refreshLabVMs(ctx, lc, mac)
		return nil, nil
	})
}

func (s *Server) handleLabVMResize(w http.ResponseWriter, r *http.Request) {
	mac, name := pathMAC(r), r.PathValue("name")
	var req struct{ CPUs, MemMiB int }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.CPUs < 1 || req.MemMiB < minVMMiB {
		writeErr(w, badRequest(`body: {"cpus":2,"memMiB":3072}`))
		return
	}
	unlock, ok := s.holdLock(w, r, labBusy, "labhost:"+mac)
	if !ok {
		return
	}
	defer unlock()
	host, ok := s.labHostOf(w, r)
	if !ok {
		return
	}
	lc, err := s.manager.LabDial(r.Context(), host)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer lc.Close()
	curMem := 0
	for _, v := range host.LabHost.VMs {
		if v.Name == name {
			curMem = v.MemMiB
		}
	}
	if req.MemMiB-curMem > freeMiB(host.LabHost.Capacity, host.LabHost.VMs) {
		writeErr(w, &statusError{Status: http.StatusUnprocessableEntity, Msg: fmt.Sprintf("resizing %s to %d MiB would overcommit the host (%d MiB total, %s reserved)", name, req.MemMiB, host.LabHost.Capacity.MemMiB, mib(host.LabHost.Capacity.Reserve()))})
		return
	}
	if err := lc.Resize(r.Context(), name, req.CPUs, req.MemMiB); err != nil {
		writeErr(w, err)
		return
	}
	s.refreshLabVMs(r.Context(), lc, mac)
	w.WriteHeader(http.StatusNoContent)
}

const labBusy = "An operation is running on this lab host; try again when it finishes."

func (s *Server) handleLabVMDelete(w http.ResponseWriter, r *http.Request) {
	mac, name := pathMAC(r), r.PathValue("name")
	unlock, ok := s.holdLock(w, r, labBusy, "labhost:"+mac)
	if !ok {
		return
	}
	defer unlock()
	host, ok := s.labHostOf(w, r)
	if !ok {
		return
	}
	if vm := s.vmRow(r.Context(), host, name); vm != nil && vm.Cluster != "" {
		writeErr(w, conflict(fmt.Sprintf("%s is a member of %s; remove it from the cluster first", name, vm.Cluster)))
		return
	}
	lc, err := s.manager.LabDial(r.Context(), host)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer lc.Close()
	if err := lc.Delete(r.Context(), name); err != nil {
		writeErr(w, err)
		return
	}
	if vm := s.vmRow(r.Context(), host, name); vm != nil {
		_ = s.store.DeleteMachine(r.Context(), vm.MAC)
	}
	s.refreshLabVMs(r.Context(), lc, mac)
	_ = s.store.Audit(r.Context(), "", "labhost.vm.delete", mac+" "+name)
	w.WriteHeader(http.StatusNoContent)
}
