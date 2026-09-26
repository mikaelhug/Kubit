package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/labhost"
	"github.com/mikael/kubit/internal/labhost/vfkit"
	"github.com/mikael/kubit/internal/oob"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
)

func (s *Server) labhostRoutes() {
	r := s.mux
	r.HandleFunc("POST /api/v1/machines/{mac}/labhost", s.handleLabProvision)
	r.HandleFunc("DELETE /api/v1/machines/{mac}/labhost", s.handleLabRelease)
	r.HandleFunc("POST /api/v1/machines/{mac}/labhost/vms", s.handleLabAddVMs)
	r.HandleFunc("POST /api/v1/machines/{mac}/labhost/vms/{name}/{action}", s.handleLabVMAction)
	r.HandleFunc("PUT /api/v1/machines/{mac}/labhost/vms/{name}", s.handleLabVMResize)
	r.HandleFunc("DELETE /api/v1/machines/{mac}/labhost/vms/{name}", s.handleLabVMDelete)
	r.HandleFunc("GET /api/v1/labhost/preseed", s.handleLabPreseed)
	r.HandleFunc("GET /api/v1/labhost/postinstall", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(labhost.PostInstall(labhost.PreseedParams{PostURL: r.URL.Query().Get("post")}.ProgressURL())))
	})
	r.HandleFunc("GET /api/v1/labhost/progress", s.handleLabProgress)
	r.HandleFunc("POST /api/v1/machines", s.handleMachineAdd)
	r.HandleFunc("GET /api/v1/labhosts/local", s.handleLabLocal)
	r.HandleFunc("POST /api/v1/labhosts", s.handleLabLocalCreate)
}

func (s *Server) handleLabPreseed(w http.ResponseWriter, r *http.Request) {
	mac := strings.ToLower(r.URL.Query().Get("mac"))
	m, err := s.store.GetMachine(r.Context(), mac)
	if err != nil {
		http.Error(w, "unknown machine", http.StatusNotFound)
		return
	}
	_, pub, err := s.store.SSHKey(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	disk := ""
	if m.LabHost != nil {
		disk = m.LabHost.Disk
	}
	if inv, ok := inventoryOf(m); disk == "" && ok {
		if cands := inv.InstallCandidates(); len(cands) > 0 {
			disk = cands[0].DevPath
		}
	}
	arch := r.URL.Query().Get("arch")
	if arch == "" {
		arch = m.Arch
	}
	out, err := labhost.Preseed(labhost.PreseedParams{Hostname: labHostname(m), Disk: disk, PublicKey: pub, PostURL: r.URL.Query().Get("post"), Timezone: r.URL.Query().Get("tz"), Arch: arch})
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte(out))
}

func labHostname(m *store.Machine) string {
	if m.Serial != "" {
		return "lab-" + strings.ToLower(strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
				return r
			}
			return -1
		}, m.Serial))
	}
	return "lab-" + strings.ReplaceAll(m.MAC[9:], ":", "")
}

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
		http.Error(w, err.Error(), code)
		return
	}
	m, err := s.store.GetMachine(r.Context(), mac)
	if err != nil {
		writeErr(w, err)
		return
	}
	if m.Cluster != "" {
		http.Error(w, "this machine is a cluster member; remove it from the cluster first", http.StatusConflict)
		return
	}
	if m.IsLabVM() {
		http.Error(w, "A lab VM cannot host VMs.", http.StatusConflict)
		return
	}
	if m.LabHost != nil && (m.LabHost.State != "error" || m.LabHost.Driver != "") {
		http.Error(w, "Already a lab host; release it first.", http.StatusConflict)
		return
	}
	c, err := s.store.MachineOOB(r.Context(), mac)
	if err != nil && !plan.Manual {
		http.Error(w, "a lab host is installed through its remote management: configure Intel AMT or a BMC on this machine, or choose to boot it yourself", http.StatusConflict)
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
		if err := probeAMT(r.Context(), mgr); err != nil {
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
				boot := store.BootLine{Kernel: fmt.Sprintf("%s/assets/debian/%s/linux", base, arch), Initrd: fmt.Sprintf("%s/assets/debian/%s/initrd.gz", base, arch), Cmdline: labhost.KernelArgs(fmt.Sprintf("%s/labhost/%s/preseed?arch=%s", base, mac, arch), labHostname(m))}
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

func (s *Server) labDesign(ctx context.Context, name string, macs []string, cpMACs map[string]bool) (*config.Cluster, error) {
	ms, err := s.designMachines(ctx, macs, true)
	if err != nil {
		return nil, err
	}
	v, _ := s.store.GetSettings(ctx)
	rng := v.DefaultMetalLB
	if len(ms) > 0 && s.labDriver(ctx, ms[0].Host) == labhost.DriverVFKit {
		rng = ""
	}
	c, _ := config.Design(name, ms, config.DesignOptions{MetalLBRange: rng, DataDisks: true})
	names := hostnamer{name: name}
	var cpIP string
	for i := range c.Spec.Nodes {
		n := &c.Spec.Nodes[i]
		cp := cpMACs[strings.ToLower(n.MAC)]
		names.assign(n, cp)
		n.Pool = string(n.Role)
		if cp && cpIP == "" {
			cpIP = n.IP
		}
	}
	sched := true
	c.Spec.ControlPlane.AllowScheduling = &sched
	if names.cps == 1 {
		c.Spec.ControlPlane.VIP = ""
		c.Spec.ControlPlane.Endpoint = "https://" + cpIP + ":6443"
	}
	return reparse(c)
}

func designDisks(inv talos.Inventory, labVM bool) []config.MachineDisk {
	var out []config.MachineDisk
	for _, d := range inv.InstallCandidates() {
		md := config.MachineDisk{DevPath: d.DevPath, SizeBytes: d.SizeBytes, Transport: d.Transport}
		if labVM && d.DevPath == "/dev/vda" {
			out = append([]config.MachineDisk{md}, out...)
		} else {
			out = append(out, md)
		}
	}
	return out
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
		sink.Emit(cluster.Info, "setup", "", "VMs will live on %s behind the host; this machine needs a route to that subnet via %s", labhost.RoutedSubnet, m.IP)
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

type addVMsRequest struct {
	Count              int      `json:"count"`
	CPUs               int      `json:"cpus"`
	MemMiB             int      `json:"memMiB"`
	DiskGiB            int      `json:"diskGiB"`
	DataGiB            int      `json:"dataGiB"`
	Prefix             string   `json:"prefix"`
	Each               []vmSize `json:"each,omitempty"`
	ControlPlanes      int      `json:"controlPlanes,omitempty"`
	ControlPlaneMemMiB int      `json:"controlPlaneMemMiB,omitempty"`
}

type vmSize struct {
	Name    string `json:"name,omitempty"`
	Role    string `json:"role,omitempty"`
	CPUs    int    `json:"cpus"`
	MemMiB  int    `json:"memMiB"`
	DiskGiB int    `json:"diskGiB"`
	DataGiB int    `json:"dataGiB"`
}

const minControlPlaneMiB = 2048

const minVMMiB = 2048

func (r addVMsRequest) sizes() []vmSize {
	if len(r.Each) > 0 {
		out := make([]vmSize, 0, len(r.Each))
		for _, v := range r.Each {
			if v.Role == "controlplane" {
				out = append(out, v)
			}
		}
		for _, v := range r.Each {
			if v.Role != "controlplane" {
				out = append(out, v)
			}
		}
		return out
	}
	out := make([]vmSize, r.Count)
	for i := range out {
		out[i] = vmSize{CPUs: r.CPUs, MemMiB: r.MemMiB, DiskGiB: r.DiskGiB, DataGiB: r.DataGiB, Role: "worker"}
		if i < r.ControlPlanes {
			out[i].Role = "controlplane"
			out[i].MemMiB = max(r.MemMiB, r.ControlPlaneMemMiB)
		}
	}
	return out
}

func (r addVMsRequest) totalMem() int {
	t := 0
	for _, v := range r.sizes() {
		t += v.MemMiB
	}
	return t
}

func (r addVMsRequest) controlPlanes() int {
	n := 0
	for _, v := range r.sizes() {
		if v.Role == "controlplane" {
			n++
		}
	}
	return n
}

func (r addVMsRequest) validate() error {
	sz := r.sizes()
	if len(sz) < 1 || len(sz) > 32 {
		return fmt.Errorf("vms: between 1 and 32 VMs")
	}
	for i, v := range sz {
		if v.CPUs < 1 || v.MemMiB < minVMMiB || v.DiskGiB < 8 || v.DataGiB < 0 {
			return fmt.Errorf("vm %d: at least 1 vCPU, %d MiB, 8 GiB disk", i+1, minVMMiB)
		}
		if v.Role == "controlplane" && v.MemMiB < minControlPlaneMiB {
			return fmt.Errorf("vm %d: a control plane needs at least %d MiB", i+1, minControlPlaneMiB)
		}
	}
	return nil
}

func (s *Server) handleLabAddVMs(w http.ResponseWriter, r *http.Request) {
	mac := pathMAC(r)
	var req addVMsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.validate() != nil {
		http.Error(w, `body: {"count":4,"cpus":2,"memMiB":3072,"diskGiB":20}; at least 1 vCPU, 2048 MiB, 8 GiB`, http.StatusBadRequest)
		return
	}
	host, err := s.store.GetMachine(r.Context(), mac)
	if err != nil {
		writeErr(w, err)
		return
	}
	if host.LabHost == nil || host.LabHost.State != "ready" {
		http.Error(w, "the lab host is not ready", http.StatusConflict)
		return
	}
	lh := host.LabHost
	if need, free := req.totalMem(), freeMiB(lh.Capacity, lh.VMs); need > free {
		http.Error(w, fmt.Sprintf("%d MiB requested, %d MiB free (host keeps %s)", need, free, mib(lh.Capacity.Reserve())), http.StatusUnprocessableEntity)
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

func freeMiB(capa labhost.Capacity, vms []labhost.VM) int {
	n := capa.MemMiB - capa.Reserve()
	for _, vm := range vms {
		n -= vm.MemMiB
	}
	return n
}

func placeholderHardware(inv talos.Inventory) []byte {
	if inv.Disks == nil {
		inv.Disks = []talos.Disk{}
	}
	if inv.Links == nil {
		inv.Links = []talos.Link{}
	}
	hw, _ := json.Marshal(inv)
	return hw
}

func mib(n int) string {
	if n%1024 == 0 {
		return fmt.Sprintf("%d GiB", n/1024)
	}
	return fmt.Sprintf("%d MiB", n)
}

func (s *Server) reconcileLabHosts(ctx context.Context) {
	rows, err := s.store.ListNodes(ctx, "")
	if err != nil {
		return
	}
	for i := range rows {
		host := &rows[i]
		if host.LabHost == nil {
			continue
		}
		switch host.LabHost.State {
		case "installing", "setup":
			if err := s.releaseLabHost(ctx, host); err != nil {
				log.Printf("lab host %s: release after restart: %v", host.MAC, err)
			}
		case "updating":
			_ = s.store.UpdateLabHost(ctx, host.MAC, func(lh *store.LabHost) { lh.State = "ready" })
		case "ready":
			if host.LabHost.Driver == labhost.DriverVFKit {
				go s.labAutostart(s.ctx, host)
			}
		}
	}
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

func uniq(v ...string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range v {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
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
		http.Error(w, `body: {"cpus":2,"memMiB":3072}`, http.StatusBadRequest)
		return
	}
	unlock, ok := s.holdLock(w, r, "labhost:"+mac, labBusy)
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
		http.Error(w, fmt.Sprintf("resizing %s to %d MiB would overcommit the host (%d MiB total, %s reserved)", name, req.MemMiB, host.LabHost.Capacity.MemMiB, mib(host.LabHost.Capacity.Reserve())), http.StatusUnprocessableEntity)
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
	unlock, ok := s.holdLock(w, r, "labhost:"+mac, labBusy)
	if !ok {
		return
	}
	defer unlock()
	host, ok := s.labHostOf(w, r)
	if !ok {
		return
	}
	if vm := s.vmRow(r.Context(), host, name); vm != nil && vm.Cluster != "" {
		http.Error(w, fmt.Sprintf("%s is a member of %s; remove it from the cluster first", name, vm.Cluster), http.StatusConflict)
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

func (s *Server) handleLabRelease(w http.ResponseWriter, r *http.Request) {
	mac := pathMAC(r)
	unlock, ok := s.holdLock(w, r, "labhost:"+mac, labBusy)
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
		http.Error(w, fmt.Sprintf("The host is %s; cancel or wait for that operation first.", host.LabHost.State), http.StatusConflict)
		return
	}
	for _, v := range host.LabHost.VMs {
		if vm, err := s.store.GetMachine(r.Context(), v.MAC); err == nil && vm.Cluster != "" {
			http.Error(w, fmt.Sprintf("%s is a member of %s; remove it first", v.Name, vm.Cluster), http.StatusConflict)
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

func (s *Server) labHostOf(w http.ResponseWriter, r *http.Request) (*store.Machine, bool) {
	host, err := s.store.GetMachine(r.Context(), pathMAC(r))
	if err != nil || host.LabHost == nil {
		http.Error(w, "not a lab host", http.StatusNotFound)
		return nil, false
	}
	return host, true
}

func (s *Server) labDriver(ctx context.Context, hostMAC string) string {
	if hostMAC == "" {
		return ""
	}
	if h, err := s.store.GetMachine(ctx, hostMAC); err == nil && h.LabHost != nil {
		return h.LabHost.Driver
	}
	return ""
}

func probeAMT(ctx context.Context, mgr oob.Manager) error {
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

func labBoot(m *store.Machine) (string, bool) {
	if m != nil && m.Provision && m.ProvisionKind == "labhost" && (m.LabHost == nil || m.LabHost.State == "installing") {
		return "debian", true
	}
	return "", false
}
