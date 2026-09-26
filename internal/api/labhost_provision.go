package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/labhost"
	"github.com/mikael/kubit/internal/labhost/libvirt"
	"github.com/mikael/kubit/internal/oob"
	"github.com/mikael/kubit/internal/store"
)

type labPlan struct {
	Manual  bool           `json:"manual,omitempty"`
	Network string         `json:"network,omitempty"`
	Disk    string         `json:"disk,omitempty"`
	VMs     *addVMsRequest `json:"vms,omitempty"`
	Cluster *struct {
		Name          string                 `json:"name"`
		ControlPlanes int                    `json:"controlPlanes"`
		SkipPlatform  bool                   `json:"skipPlatform"`
		Repository    *config.FluxRepository `json:"repository,omitempty"`
	} `json:"cluster,omitempty"`
}

func (s *Server) handleLabProvision(w http.ResponseWriter, r *http.Request) {
	mac := pathMAC(r)
	var plan labPlan
	if !decodeOptionalJSON(w, r, &plan) {
		return
	}
	if code, err := s.checkLabPlan(r.Context(), &plan); err != nil {
		writeErr(w, &statusError{code, err.Error()})
		return
	}
	m, err := s.store.GetMachine(r.Context(), mac)
	if err != nil {
		writeErr(w, err)
		return
	}
	if m.Cluster != "" {
		writeErr(w, &statusError{http.StatusConflict, "this machine is a cluster member; remove it from the cluster first"})
		return
	}
	if m.IsLabVM() {
		writeErr(w, &statusError{http.StatusConflict, "A lab VM cannot host VMs."})
		return
	}
	if m.LabHost != nil && (m.LabHost.State != "error" || m.LabHost.Driver != "") {
		writeErr(w, &statusError{http.StatusConflict, "Already a lab host; release it first."})
		return
	}
	c, err := s.store.MachineOOB(r.Context(), mac)
	if err != nil && !plan.Manual {
		writeErr(w, &statusError{http.StatusConflict, "a lab host is installed through its remote management: configure Intel AMT or a BMC on this machine, or choose to boot it yourself"})
		return
	}
	if !s.pxeRunning(r.Context()) {
		pxeDown(w, pxeCommand(r.Host, plan.Manual))
		return
	}
	if !plan.Manual {
		if st, err := s.pxeStatus(r.Context()); err == nil && st.IP != "" && differentSubnet(st.IP, c.Host) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": fmt.Sprintf("The PXE server is on %s, not on %s's segment — the machine's network boot request cannot reach it. Run kubit pxe on the interface facing the machine.", st.IP, c.Host), "code": "pxe-segment"})
			return
		}
		mgr, err := oob.Open(*c)
		if err != nil {
			writeErr(w, err)
			return
		}
		if err := probeOOB(r.Context(), mgr); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": fmt.Sprintf("%s at %s is not answering (%v). Check the machine has standby power and the credentials are right, then try again.", oob.Label(c.Type), c.Host, err), "code": "amt-down"})
			return
		}
	}
	kind := "labhost.provision"
	if plan.Cluster != nil {
		kind = "labhost.cluster"
	}
	s.startOp(w, "labhost:"+mac, kind, map[string]any{"mac": mac, "plan": plan}, func(ctx context.Context, sink cluster.Sink) (result any, err error) {
		defer func() {
			if err == nil {
				return
			}
			sink.Emit(cluster.Warn, "", "", "provision failed, releasing the host: %v", err)
			rctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if host, e := s.store.GetMachine(rctx, mac); e == nil {
				if rerr := s.releaseLabHost(rctx, host); rerr != nil {
					sink.Emit(cluster.Warn, "", "", "release: %v", rerr)
				}
			} else if perr := s.store.SetMachineProvision(rctx, mac, false); perr != nil {
				sink.Emit(cluster.Warn, "", "", "clear the network-boot arm: %v", perr)
			}
		}()
		armWhat := "Arm the Debian install; you boot the machine"
		if !plan.Manual {
			armWhat = "Arm a Debian network boot and reset via " + oob.Label(c.Type)
		}
		steps := cluster.Steps("arm", armWhat)
		if !plan.Manual {
			steps = append(steps, cluster.Steps("boot", "Network boot request seen", "ipxe", "Installer kernel fetched")...)
		}
		steps = append(steps, cluster.Steps("installer", "Installer running", "install", "Unattended Debian install", "ssh", "Reboot into Debian, SSH", "setup", "Verify KVM, record capacity, fetch Talos boot assets")...)
		if plan.VMs != nil {
			steps = append(steps, cluster.Steps("define", "Create the VMs", "vmboot", "Wait for Talos maintenance mode")...)
		}
		if plan.Cluster != nil {
			steps = append(steps, cluster.Steps("cluster", "Design and create the cluster")...)
		}
		sink.Plan(steps...)
		sink.Begin("arm")
		if _, _, err := s.store.SSHKey(ctx); err != nil {
			return nil, err
		}
		if err := s.store.SetMachineProvision(ctx, mac, true, "labhost"); err != nil {
			return nil, err
		}
		if err := s.store.SetLabHost(ctx, mac, &store.LabHost{State: "installing", Network: plan.Network, Disk: plan.Disk}); err != nil {
			return nil, err
		}
		_ = s.store.SetNodeState(ctx, m.IP, "labhost")
		if plan.Manual {
			if st, err := s.pxeStatus(ctx); err == nil {
				base := st.BaseURL()
				arch := m.Arch
				if arch == "" {
					arch = "amd64"
				}
				boot := store.BootLine{Kernel: fmt.Sprintf("%s/assets/debian/%s/linux", base, arch), Initrd: fmt.Sprintf("%s/assets/debian/%s/initrd.gz", base, arch), Cmdline: libvirt.KernelArgs(fmt.Sprintf("%s/labhost/%s/preseed?arch=%s", base, mac, arch), labHostname(m))}
				if err := s.store.UpdateLabHost(ctx, mac, func(l *store.LabHost) { l.Boot = &boot }); err != nil {
					return nil, err
				}
				sink.Emit(cluster.Info, "arm", "", "boot the machine now with kernel %s, initrd %s, cmdline: %s", boot.Kernel, boot.Initrd, boot.Cmdline)
			}
		} else {
			mgr, err := oob.Open(*c, oob.WithTrace(func(line string) {
				sink.Emit(cluster.Info, "arm", "", "%s: %s", c.Type, line)
			}))
			if err != nil {
				return nil, err
			}
			if err := mgr.Power(ctx, oob.BootPXE); err != nil {
				return nil, err
			}
			sink.Emit(cluster.Info, "arm", "", "reset via %s; kubit pxe will hand it the Debian installer (hostname %s)", oob.Label(c.Type), labHostname(m))
		}
		sink.End("arm")

		lc, err := s.labWaitInstall(ctx, m, plan.Manual, sink)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				err = fmt.Errorf("install cancelled: %w", err)
			}
			return nil, err
		}
		defer lc.Close()
		sink.Begin("setup")
		if fresh, err := s.store.GetMachine(ctx, mac); err == nil {
			fresh.IP = m.IP
			m = fresh
		}
		lh, err := s.labSetup(ctx, lc, m, sink)
		if err != nil {
			return nil, err
		}
		_ = s.store.UpsertNode(ctx, store.NodeRow{MAC: mac, IP: m.IP, Source: "labhost", State: "labhost", Hostname: lh.Capacity.Hostname, Arch: lh.Capacity.Arch})
		_ = s.store.SetMachineProvision(ctx, mac, false)
		_ = s.store.Audit(ctx, "", "labhost.provision", mac)
		sink.Emit(cluster.Done, "setup", "", "lab host ready: %d CPUs, %d MiB RAM, %d GiB free for VMs", lh.Capacity.CPUs, lh.Capacity.MemMiB, lh.Capacity.DiskGiB)
		sink.End("setup")
		if plan.VMs == nil {
			return lh, nil
		}
		return s.labRunPlan(ctx, mac, plan, sink)
	})
}

func (s *Server) checkLabPlan(ctx context.Context, plan *labPlan) (int, error) {
	if plan.VMs != nil {
		if plan.Cluster != nil && len(plan.VMs.Each) == 0 {
			plan.VMs.ControlPlanes, plan.VMs.ControlPlaneMemMiB = plan.Cluster.ControlPlanes, minControlPlaneMiB
		}
		if err := plan.VMs.validate(); err != nil {
			return http.StatusBadRequest, err
		}
	}
	if plan.Network != "" && plan.Network != "bridge" && plan.Network != "routed" {
		return http.StatusBadRequest, errors.New("network must be bridge or routed")
	}
	if plan.Disk != "" && !strings.HasPrefix(plan.Disk, "/dev/") {
		return http.StatusBadRequest, errors.New("disk must be a /dev path")
	}
	if plan.Cluster != nil {
		if r := plan.Cluster.Repository; r != nil && r.URL == "" {
			plan.Cluster.Repository = nil
		}
		if r := plan.Cluster.Repository; r != nil {
			r.Default()
			if err := r.Validate(); err != nil {
				return http.StatusBadRequest, err
			}
		}
		if plan.VMs == nil {
			return http.StatusBadRequest, errors.New("a cluster needs vms")
		}
		if len(plan.VMs.Each) > 0 {
			plan.Cluster.ControlPlanes = plan.VMs.controlPlanes()
		}
		if plan.Cluster.ControlPlanes != 1 && plan.Cluster.ControlPlanes != 3 {
			return http.StatusBadRequest, errors.New("the cluster needs 1 or 3 control planes")
		}
		if plan.Cluster.ControlPlanes > len(plan.VMs.sizes()) {
			return http.StatusBadRequest, errors.New("more control planes than VMs")
		}
		if _, err := s.store.GetCluster(ctx, plan.Cluster.Name); err == nil {
			return http.StatusConflict, errors.New("a cluster with that name exists")
		}
	}
	return 0, nil
}

func (s *Server) labRunPlan(ctx context.Context, mac string, plan labPlan, sink cluster.Sink) (any, error) {
	host, err := s.store.GetMachine(ctx, mac)
	if err != nil {
		return nil, err
	}
	sink.Begin("define")
	macs, err := s.labAddVMs(ctx, host, *plan.VMs, sink)
	if err != nil {
		return nil, err
	}
	sink.End("vmboot")
	if plan.Cluster == nil {
		return macs, nil
	}
	sink.Begin("cluster")
	cpMACs := map[string]bool{}
	sizes := plan.VMs.sizes()
	for i, mac := range macs {
		if i < len(sizes) && sizes[i].Role == "controlplane" {
			cpMACs[strings.ToLower(mac)] = true
		}
	}
	c, err := s.labDesign(ctx, plan.Cluster.Name, macs, cpMACs)
	if err != nil {
		return nil, err
	}
	c.Spec.Platform.Flux.Repository = plan.Cluster.Repository
	skip := plan.Cluster.SkipPlatform
	opID, err := s.startCreate(c, skip, map[string]any{"yaml": mustYAML(c), "skipPlatform": skip, "from": "labhost"})
	if err != nil {
		return nil, err
	}
	sink.Emit(cluster.Done, "cluster", "", "cluster %s creation started as operation #%d (%d control planes, %d workers)", c.Metadata.Name, opID, len(c.ControlPlanes()), len(c.Workers()))
	sink.End("cluster")
	return map[string]any{"cluster": c.Metadata.Name, "operationId": opID}, nil
}

func (s *Server) labSetup(ctx context.Context, lc labhost.Driver, m *store.Machine, sink cluster.Sink) (*store.LabHost, error) {
	lh := &store.LabHost{State: "setup"}
	if m.LabHost != nil {
		lh.Index, lh.Network, lh.Driver = m.LabHost.Index, m.LabHost.Network, m.LabHost.Driver
	}
	if lh.Network == "routed" {
		rt, ok := lc.(labhost.Router)
		if !ok {
			return lh, fmt.Errorf("this lab host has no routed VM network")
		}
		if err := rt.EnsureRouted(ctx); err != nil {
			return lh, fmt.Errorf("routed VM network: %w", err)
		}
		sink.Emit(cluster.Info, "setup", "", "VMs will live on %s behind the host; this machine needs a route to that subnet via %s", libvirt.RoutedSubnet, m.IP)
	}
	capa, err := lc.Capacity(ctx)
	if err != nil {
		return lh, err
	}
	lh.Capacity = capa
	if capa.Problem != "" {
		return lh, errors.New(strings.TrimSpace(capa.Problem + " " + capa.Command))
	}
	if lh.Driver == "" && !capa.KVM {
		if os.Getenv("KUBIT_LAB_ALLOW_TCG") == "" {
			return lh, fmt.Errorf("/dev/kvm is missing on the host: enable VT-x/AMD-V in the BIOS")
		}
		sink.Emit(cluster.Warn, "setup", "", "/dev/kvm missing; KUBIT_LAB_ALLOW_TCG is set, VMs will run under software emulation (slow)")
	}
	if lh.Driver == "" && capa.Bridge == "" {
		return lh, fmt.Errorf("no bridge on the host: the install did not create br0")
	}
	version := s.latestStableTalos(ctx)
	if version == "" {
		version = defaultTalosVersion()
	}
	schematic, err := s.manager.Factory.CreateSchematic(ctx, nil)
	if err != nil {
		return lh, err
	}
	sink.Emit(cluster.Info, "setup", "", "fetching Talos %s boot assets for %s onto the host", version, capa.Arch)
	boot, err := lc.EnsureTalosBoot(ctx, s.manager.Factory, schematic, version, capa.Arch)
	if err != nil {
		return lh, fmt.Errorf("Talos boot assets: %w", err)
	}
	lh.Talos, lh.Schematic, lh.Kernel, lh.Initrd, lh.ISO = version, schematic, boot.Kernel, boot.Initrd, boot.ISO
	lh.VMs, _ = lc.List(ctx)
	lh.State = "ready"
	return lh, s.store.SetLabHost(ctx, m.MAC, lh)
}

func (s *Server) handleLabRelease(w http.ResponseWriter, r *http.Request) {
	mac := pathMAC(r)
	unlock, ok := s.holdLock(w, r, labBusy, "labhost:"+mac)
	if !ok {
		return
	}
	defer unlock()
	host, ok := s.labHostOf(w, r)
	if !ok {
		return
	}
	switch host.LabHost.State {
	case "installing", "setup", "updating":
		writeErr(w, &statusError{http.StatusConflict, fmt.Sprintf("The host is %s; cancel or wait for that operation first.", host.LabHost.State)})
		return
	}
	for _, v := range host.LabHost.VMs {
		if vm, err := s.store.GetMachine(r.Context(), v.MAC); err == nil && vm.Cluster != "" {
			writeErr(w, &statusError{http.StatusConflict, fmt.Sprintf("%s is a member of %s; remove it first", v.Name, vm.Cluster)})
			return
		}
	}
	if err := s.releaseLabHost(r.Context(), host); err != nil {
		writeErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), "", "labhost.release", mac)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) releaseLabHost(ctx context.Context, host *store.Machine) error {
	local := host.LabHost != nil && host.LabHost.Driver == labhost.DriverVFKit
	var errs []error
	if host.LabHost != nil && (host.LabHost.State == "ready" || len(host.LabHost.VMs) > 0 || local) {
		dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		if lc, err := s.manager.LabDial(dctx, host); err == nil {
			vms := host.LabHost.VMs
			if local {
				if listed, err := lc.List(dctx); err == nil {
					vms = append(listed, vms...)
				}
			}
			done := map[string]bool{}
			for _, v := range vms {
				if done[v.Name] {
					continue
				}
				done[v.Name] = true
				if err := lc.Delete(dctx, v.Name); err != nil {
					log.Printf("lab host %s: delete VM %s: %v", host.MAC, v.Name, err)
				}
				if err := s.store.DeleteMachine(ctx, v.MAC); err != nil {
					errs = append(errs, fmt.Errorf("forget VM %s: %w", v.Name, err))
				}
			}
			lc.Close()
		}
		cancel()
	}
	if err := s.store.SetMachineProvision(ctx, host.MAC, false); err != nil {
		errs = append(errs, fmt.Errorf("clear the network-boot arm: %w", err))
	}
	if err := s.store.DeleteLabHostHistory(ctx, host.MAC); err != nil {
		errs = append(errs, fmt.Errorf("drop history: %w", err))
	}
	if local {
		if err := s.store.DeleteMachine(ctx, host.MAC); err != nil {
			errs = append(errs, fmt.Errorf("forget the host: %w", err))
		}
		return errors.Join(errs...)
	}
	if err := s.store.SetLabHost(ctx, host.MAC, nil); err != nil {
		errs = append(errs, fmt.Errorf("drop the lab-host record: %w", err))
	}
	if err := s.store.SetNodeState(ctx, host.IP, "unknown"); err != nil {
		errs = append(errs, fmt.Errorf("reset the machine state: %w", err))
	}
	return errors.Join(errs...)
}

func probeOOB(ctx context.Context, mgr oob.Manager) error {
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		pctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		_, err = mgr.Probe(pctx)
		cancel()
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return err
}

func differentSubnet(a, b string) bool {
	x, e1 := netip.ParseAddr(a)
	y, e2 := netip.ParseAddr(b)
	if e1 != nil || e2 != nil || !x.Is4() || !y.Is4() {
		return false
	}
	xa, ya := x.As4(), y.As4()
	return xa[0] != ya[0] || xa[1] != ya[1] || xa[2] != ya[2]
}
