package api

import (
	"context"
	"encoding/json"
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
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
)

func (s *Server) handleLabAddVMs(w http.ResponseWriter, r *http.Request) {
	mac := pathMAC(r)
	var req addVMsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.validate() != nil {
		writeErr(w, &statusError{http.StatusBadRequest, `body: {"count":4,"cpus":2,"memMiB":3072,"diskGiB":20}; at least 1 vCPU, 2048 MiB, 8 GiB`})
		return
	}
	host, err := s.store.GetMachine(r.Context(), mac)
	if err != nil {
		writeErr(w, err)
		return
	}
	if host.LabHost == nil || host.LabHost.State != "ready" {
		writeErr(w, &statusError{http.StatusConflict, "the lab host is not ready"})
		return
	}
	lh := host.LabHost
	if need, free := req.totalMem(), freeMiB(lh.Capacity, lh.VMs); need > free {
		writeErr(w, &statusError{http.StatusUnprocessableEntity, fmt.Sprintf("%d MiB requested, %d MiB free (host keeps %s)", need, free, mib(lh.Capacity.Reserve()))})
		return
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
	next := len(existing) + 1
	prefix := req.Prefix
	if prefix == "" {
		prefix = "vm"
	}
	var created, createdMACs []string
	for _, size := range req.sizes() {
		name := size.Name
		if name == "" || nameTaken(existing, name) {
			name = fmt.Sprintf("%s-%02d", prefix, next)
			for nameTaken(existing, name) {
				next++
				name = fmt.Sprintf("%s-%02d", prefix, next)
			}
		}
		spec := labhost.VMSpec{Name: name, MAC: labhost.MAC(lh.Index, next), CPUs: size.CPUs, MemMiB: size.MemMiB, DiskGiB: size.DiskGiB, DataGiB: size.DataGiB, Kernel: lh.Kernel, Initrd: lh.Initrd, ISO: lh.ISO, Arch: lh.Capacity.Arch, Bridge: lh.Capacity.Bridge, Routed: lh.Network == "routed", TCG: !lh.Capacity.KVM}
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
		sink.Emit(cluster.Info, "define", name, "defined and started: %s, %d vCPU, %d MiB, %d GiB%s, %s", size.Role, size.CPUs, size.MemMiB, size.DiskGiB, map[bool]string{true: fmt.Sprintf(" + %d GiB data", size.DataGiB), false: ""}[size.DataGiB > 0], spec.MAC)
		next++
	}
	if vms, err := lc.List(ctx); err == nil {
		lh.VMs = vms
	}
	_ = s.store.UpdateLabHost(ctx, mac, func(l *store.LabHost) { l.VMs = lh.VMs })
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
			return nil, ctx.Err()
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
		return nil, fmt.Errorf("%s did not reach Talos maintenance mode within 6 minutes (%s)", strings.Join(names, ", "), hint)
	}
	_ = s.store.Audit(ctx, "", "labhost.vms", fmt.Sprintf("%s +%d", mac, len(created)))
	sink.Emit(cluster.Done, "vmboot", "", "%d VM(s) in maintenance mode, ready to be picked for a cluster", len(created))
	return createdMACs, nil
}

func (s *Server) refreshLabVMs(ctx context.Context, lc labhost.Driver, mac string) {
	if vms, err := lc.List(ctx); err == nil {
		_ = s.store.UpdateLabHost(ctx, mac, func(lh *store.LabHost) { lh.VMs = vms })
	}
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
		writeErr(w, &statusError{http.StatusBadRequest, `body: {"cpus":2,"memMiB":3072}`})
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
		writeErr(w, &statusError{http.StatusUnprocessableEntity, fmt.Sprintf("resizing %s to %d MiB would overcommit the host (%d MiB total, %s reserved)", name, req.MemMiB, host.LabHost.Capacity.MemMiB, mib(host.LabHost.Capacity.Reserve()))})
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
		writeErr(w, &statusError{http.StatusConflict, fmt.Sprintf("%s is a member of %s; remove it from the cluster first", name, vm.Cluster)})
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
