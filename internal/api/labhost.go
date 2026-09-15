package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/labhost"
	"github.com/mikael/kubit/internal/oob"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
)

func (s *Server) labhostRoutes() {
	r := s.mux
	r.HandleFunc("POST /api/v1/machines/{mac}/labhost", s.handleLabProvision)
	r.HandleFunc("GET /api/v1/machines/{mac}/labhost", s.handleLabGet)
	r.HandleFunc("DELETE /api/v1/machines/{mac}/labhost", s.handleLabRelease)
	r.HandleFunc("POST /api/v1/machines/{mac}/labhost/vms", s.handleLabAddVMs)
	r.HandleFunc("POST /api/v1/machines/{mac}/labhost/vms/{name}/{action}", s.handleLabVMAction)
	r.HandleFunc("PUT /api/v1/machines/{mac}/labhost/vms/{name}", s.handleLabVMResize)
	r.HandleFunc("DELETE /api/v1/machines/{mac}/labhost/vms/{name}", s.handleLabVMDelete)
	r.HandleFunc("GET /api/v1/labhost/preseed", s.handleLabPreseed)
	r.HandleFunc("GET /api/v1/labhost/postinstall", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(labhost.PostInstall))
	})
}

// handleLabPreseed is fetched (via the pxe process) by the Debian installer.
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
	if len(m.Hardware) > 2 {
		var inv talos.Inventory
		if json.Unmarshal(m.Hardware, &inv) == nil {
			if cands := inv.InstallCandidates(); len(cands) > 0 {
				sort.Slice(cands, func(i, j int) bool { return cands[i].SizeBytes > cands[j].SizeBytes })
				disk = cands[0].DevPath
			}
		}
	}
	out, err := labhost.Preseed(labhost.PreseedParams{Hostname: labHostname(m), Disk: disk, PublicKey: pub, PostURL: r.URL.Query().Get("post"), Timezone: r.URL.Query().Get("tz")})
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

// labPlan is the optional "and then" of a provision: carve VMs and create a cluster
// from them, so the operator can start it and come back to a running cluster.
type labPlan struct {
	VMs     *addVMsRequest `json:"vms,omitempty"`
	Cluster *struct {
		Name          string `json:"name"`
		ControlPlanes int    `json:"controlPlanes"` // 1 or 3
		SkipPlatform  bool   `json:"skipPlatform"`
	} `json:"cluster,omitempty"`
}

// handleLabProvision: arm a Debian network boot, reset via AMT, wait for SSH, set up —
// then, if a plan was given, add the VMs and create the cluster in the same run.
func (s *Server) handleLabProvision(w http.ResponseWriter, r *http.Request) {
	mac := strings.ToLower(r.PathValue("mac"))
	var plan labPlan
	_ = json.NewDecoder(r.Body).Decode(&plan)
	if plan.VMs != nil && (plan.VMs.Count < 1 || plan.VMs.CPUs < 1 || plan.VMs.MemMiB < 1024 || plan.VMs.DiskGiB < 8 || plan.VMs.DataGiB < 0) {
		http.Error(w, "vms: at least 1 VM, 1 vCPU, 1024 MiB, 8 GiB", http.StatusBadRequest)
		return
	}
	if plan.Cluster != nil {
		if plan.VMs == nil {
			http.Error(w, "a cluster needs vms", http.StatusBadRequest)
			return
		}
		if plan.Cluster.ControlPlanes != 1 && plan.Cluster.ControlPlanes != 3 {
			http.Error(w, "controlPlanes must be 1 or 3", http.StatusBadRequest)
			return
		}
		if plan.Cluster.ControlPlanes > plan.VMs.Count {
			http.Error(w, "more control planes than VMs", http.StatusBadRequest)
			return
		}
		if _, err := s.store.GetCluster(r.Context(), plan.Cluster.Name); err == nil {
			http.Error(w, "a cluster with that name exists", http.StatusConflict)
			return
		}
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
	c, err := s.store.MachineOOB(r.Context(), mac)
	if err != nil {
		http.Error(w, "a lab host is installed through its remote management: configure Intel AMT on this machine first", http.StatusConflict)
		return
	}
	if !s.pxeRunning(r.Context()) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "The PXE server is not running, so the machine would find nothing to boot. Start it in a terminal (it can stay open): " + pxeCommand(r.Host), "code": "pxe-down", "command": pxeCommand(r.Host)})
		return
	}
	kind := "labhost.provision"
	if plan.Cluster != nil {
		kind = "labhost.cluster"
	}
	id, err := s.runOperation("", kind, map[string]any{"mac": mac, "plan": plan}, func(ctx contextT, sink clusterSink) (result any, err error) {
		defer func() {
			if err != nil {
				_ = s.store.SetMachineProvision(context.Background(), mac, false)
			}
		}()
		steps := cluster.Steps("arm", "Arm a Debian network boot and reset via AMT", "install", "Unattended Debian install", "setup", "Verify KVM, record capacity, fetch Talos boot assets")
		if plan.VMs != nil {
			steps = append(steps, cluster.Steps("define", "Create the VMs", "boot", "Wait for Talos maintenance mode")...)
		}
		if plan.Cluster != nil {
			steps = append(steps, cluster.Steps("cluster", "Design and create the cluster")...)
		}
		sink(clusterEvent{Time: time.Now(), Kind: "steps", Level: "info", Steps: steps})
		sink(clusterEvent{Time: time.Now(), Kind: "step", Step: "arm", Status: cluster.StepRunning})
		if _, _, err := s.store.SSHKey(ctx); err != nil {
			return nil, err
		}
		if err := s.store.SetMachineProvision(ctx, mac, true, "labhost"); err != nil {
			return nil, err
		}
		if err := s.store.SetLabHost(ctx, mac, &store.LabHost{State: "installing", Index: s.store.NextLabHostIndex(ctx)}); err != nil {
			return nil, err
		}
		_ = s.store.SetNodeState(ctx, m.IP, "labhost")
		mgr, err := oob.Open(*c)
		if err != nil {
			return nil, err
		}
		if err := mgr.Power(ctx, oob.BootPXE); err != nil {
			return nil, err
		}
		sink(clusterEvent{Time: time.Now(), Kind: "log", Level: "info", Step: "arm", Message: "reset via AMT; kubit pxe will hand it the Debian installer (hostname " + labHostname(m) + ")"})
		sink(clusterEvent{Time: time.Now(), Kind: "step", Step: "arm", Status: cluster.StepDone})

		sink(clusterEvent{Time: time.Now(), Kind: "step", Step: "install", Status: cluster.StepRunning})
		priv, _, _ := s.store.SSHKey(ctx)
		var lc *labhost.Client
		deadline := time.Now().Add(30 * time.Minute)
		for time.Now().Before(deadline) {
			for _, ip := range uniq(m.IP, c.Host) {
				d := net.Dialer{Timeout: 2 * time.Second}
				if conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, "22")); err == nil {
					conn.Close()
					if cl, err := labhost.Dial(ctx, ip, priv); err == nil {
						if _, err := cl.Run(ctx, "test -f /var/lib/kubit/READY"); err == nil {
							lc = cl
							m.IP = ip
							break
						}
						cl.Close()
					}
				}
			}
			if lc != nil {
				break
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(10 * time.Second):
			}
		}
		if lc == nil {
			_ = s.store.SetLabHost(ctx, mac, &store.LabHost{State: "error", Error: "no SSH within 30 minutes"})
			return nil, fmt.Errorf("the host did not come up with SSH within 30 minutes; check the Network boot page (did the machine PXE-boot?) and the installer console")
		}
		defer lc.Close()
		sink(clusterEvent{Time: time.Now(), Kind: "log", Level: "info", Step: "install", Node: m.IP, Message: "Debian installed; SSH answers as " + labhost.User})
		sink(clusterEvent{Time: time.Now(), Kind: "step", Step: "install", Status: cluster.StepDone})

		sink(clusterEvent{Time: time.Now(), Kind: "step", Step: "setup", Status: cluster.StepRunning})
		lh, err := s.labSetup(ctx, lc, m, sink)
		if err != nil {
			_ = s.store.SetLabHost(ctx, mac, &store.LabHost{State: "error", Error: err.Error(), Index: lh.Index})
			return nil, err
		}
		_ = s.store.UpsertNode(ctx, store.NodeRow{MAC: mac, IP: m.IP, Source: "labhost", State: "labhost", Hostname: lh.Capacity.Hostname, Arch: lh.Capacity.Arch})
		_ = s.store.Audit(ctx, "", "labhost.provision", mac)
		sink(clusterEvent{Time: time.Now(), Kind: "log", Level: "done", Step: "setup", Message: fmt.Sprintf("lab host ready: %d CPUs, %d MiB RAM, %d GiB free for VMs", lh.Capacity.CPUs, lh.Capacity.MemMiB, lh.Capacity.DiskGiB)})
		sink(clusterEvent{Time: time.Now(), Kind: "step", Step: "setup", Status: cluster.StepDone})
		if plan.VMs == nil {
			return lh, nil
		}
		host, err := s.store.GetMachine(ctx, mac)
		if err != nil {
			return nil, err
		}
		sink(clusterEvent{Time: time.Now(), Kind: "step", Step: "define", Status: cluster.StepRunning})
		macs, err := s.labAddVMs(ctx, host, *plan.VMs, sink)
		if err != nil {
			return nil, err
		}
		sink(clusterEvent{Time: time.Now(), Kind: "step", Step: "boot", Status: cluster.StepDone})
		if plan.Cluster == nil {
			return macs, nil
		}
		// The cluster is its own operation (per-cluster lock, its own steps); this one
		// ends once it is started.
		sink(clusterEvent{Time: time.Now(), Kind: "step", Step: "cluster", Status: cluster.StepRunning})
		c, err := s.labDesign(ctx, plan.Cluster.Name, macs, plan.Cluster.ControlPlanes)
		if err != nil {
			return nil, err
		}
		skip := plan.Cluster.SkipPlatform
		opID, err := s.runOperation(c.Metadata.Name, "cluster.create", map[string]any{"yaml": mustYAML(c), "skipPlatform": skip, "from": "labhost"}, func(ctx contextT, sink clusterSink) (any, error) {
			if err := s.manager.Create(ctx, c, sink); err != nil {
				return nil, err
			}
			if skip {
				return nil, nil
			}
			return nil, s.manager.ApplyPlatform(ctx, c.Metadata.Name, sink)
		})
		if err != nil {
			return nil, err
		}
		sink(clusterEvent{Time: time.Now(), Kind: "log", Level: "done", Step: "cluster", Message: fmt.Sprintf("cluster %s creation started as operation #%d (%d control planes, %d workers)", c.Metadata.Name, opID, len(c.ControlPlanes()), len(c.Workers()))})
		sink(clusterEvent{Time: time.Now(), Kind: "step", Step: "cluster", Status: cluster.StepDone})
		return map[string]any{"cluster": c.Metadata.Name, "operationId": opID}, nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"operationId": id})
}

// labDesign proposes a cluster for the lab VMs with the requested control-plane count
// and hostnames <name>-cp-NN / <name>-worker-NN.
func (s *Server) labDesign(ctx contextT, name string, macs []string, controlPlanes int) (*config.Cluster, error) {
	var ms []config.Machine
	for _, mac := range macs {
		m, err := s.store.GetMachine(ctx, mac)
		if err != nil {
			return nil, err
		}
		var inv talos.Inventory
		_ = json.Unmarshal(m.Hardware, &inv)
		cm := config.Machine{IP: m.IP, MAC: m.MAC, UUID: m.UUID, Arch: config.Arch(m.Arch), CPUs: inv.CPUs, MemBytes: inv.MemoryBytes, KVM: inv.KVM, Virtual: true, Host: m.Host, Model: "Kubit lab VM"}
		if cm.Arch == "" {
			cm.Arch = config.ArchAMD64
		}
		cm.Disks = designDisks(inv, true)
		ms = append(ms, cm)
	}
	v, _ := s.store.GetSettings(ctx)
	c, _ := config.Design(name, ms, config.DesignOptions{MetalLBRange: v.DefaultMetalLB, DataDisks: true})
	cps, workers := 0, 0
	for i := range c.Spec.Nodes {
		n := &c.Spec.Nodes[i]
		if i < controlPlanes {
			cps++
			n.Pool, n.Role, n.Hostname = "controlplane", config.RoleControlPlane, fmt.Sprintf("%s-cp-%02d", name, cps)
		} else {
			workers++
			n.Pool, n.Role, n.Hostname = "worker", config.RoleWorker, fmt.Sprintf("%s-worker-%02d", name, workers)
		}
	}
	sched := true
	c.Spec.ControlPlane.AllowScheduling = &sched
	if controlPlanes == 1 {
		c.Spec.ControlPlane.VIP = ""
		c.Spec.ControlPlane.Endpoint = "https://" + c.Spec.Nodes[0].IP + ":6443"
	}
	// Round-trip through Parse so every default (endpoint, pools, versions) is filled
	// exactly as for a declaration written by hand.
	b, err := c.Marshal()
	if err != nil {
		return nil, err
	}
	return config.Parse(b)
}

// designDisks orders install candidates for Design: largest first, except that a
// lab VM boots from its first disk whatever the sizes, so /dev/vda leads and a
// larger data disk stays data.
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

// labSetup verifies the host and fetches the Talos boot assets; reused by re-setup.
func (s *Server) labSetup(ctx context.Context, lc *labhost.Client, m *store.Machine, sink clusterSink) (*store.LabHost, error) {
	lh := &store.LabHost{State: "setup"}
	if m.LabHost != nil {
		lh.Index = m.LabHost.Index
	}
	if lh.Index == 0 {
		lh.Index = s.store.NextLabHostIndex(ctx)
	}
	capa, err := lc.Capacity(ctx)
	if err != nil {
		return lh, err
	}
	lh.Capacity = capa
	if !capa.KVM {
		return lh, fmt.Errorf("/dev/kvm is missing on the host: enable VT-x/AMD-V in the BIOS")
	}
	if capa.Bridge == "" {
		return lh, fmt.Errorf("no bridge on the host: the install did not create br0")
	}
	version := s.latestStableTalos(ctx)
	if version == "" {
		version = defaultTalosVersion()
	}
	schematic, err := s.manager.Factory.CreateSchematic(ctx, []string{"siderolabs/gvisor"})
	if err != nil {
		return lh, err
	}
	if sink != nil {
		sink(clusterEvent{Time: time.Now(), Kind: "log", Level: "info", Step: "setup", Message: fmt.Sprintf("fetching Talos %s boot assets for %s onto the host", version, capa.Arch)})
	}
	kernel, initrd, err := lc.EnsureTalosBoot(ctx, s.manager.Factory.BaseURL, schematic, version, capa.Arch)
	if err != nil {
		return lh, fmt.Errorf("Talos boot assets: %w", err)
	}
	lh.Talos, lh.Schematic, lh.Kernel, lh.Initrd = version, schematic, kernel, initrd
	lh.VMs, _ = lc.List(ctx)
	lh.State = "ready"
	return lh, s.store.SetLabHost(ctx, m.MAC, lh)
}

func (s *Server) handleLabGet(w http.ResponseWriter, r *http.Request) {
	m, err := s.store.GetMachine(r.Context(), r.PathValue("mac"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if m.LabHost == nil {
		http.Error(w, "not a lab host", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, m.LabHost)
}

type addVMsRequest struct {
	Count   int    `json:"count"`
	CPUs    int    `json:"cpus"`
	MemMiB  int    `json:"memMiB"`
	DiskGiB int    `json:"diskGiB"`
	DataGiB int    `json:"dataGiB"` // 0 = no data disk
	Prefix  string `json:"prefix"`
}

// handleLabAddVMs defines and starts VMs, records each as a machine, and waits for
// Talos maintenance mode on each.
func (s *Server) handleLabAddVMs(w http.ResponseWriter, r *http.Request) {
	mac := strings.ToLower(r.PathValue("mac"))
	var req addVMsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Count < 1 || req.CPUs < 1 || req.MemMiB < 1024 || req.DiskGiB < 8 || req.DataGiB < 0 {
		http.Error(w, `body: {"count":4,"cpus":2,"memMiB":3072,"diskGiB":20}; at least 1 vCPU, 1024 MiB, 8 GiB`, http.StatusBadRequest)
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
	if need := req.Count * req.MemMiB; need > lh.Capacity.MemMiB-2048-usedMem(lh) {
		http.Error(w, fmt.Sprintf("%d MiB requested, %d MiB free (host keeps 2 GiB)", need, lh.Capacity.MemMiB-2048-usedMem(lh)), http.StatusUnprocessableEntity)
		return
	}
	id, err := s.runOperation("", "labhost.vms", req, func(ctx contextT, sink clusterSink) (any, error) {
		return s.labAddVMs(ctx, host, req, sink)
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"operationId": id})
}

// labAddVMs defines, starts and waits for the VMs; returns the MACs of the new VMs.
func (s *Server) labAddVMs(ctx contextT, host *store.Machine, req addVMsRequest, sink clusterSink) ([]string, error) {
	lh := host.LabHost
	mac := host.MAC
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
	for i := 0; i < req.Count; i++ {
		name := fmt.Sprintf("%s-%02d", prefix, next)
		for nameTaken(existing, name) {
			next++
			name = fmt.Sprintf("%s-%02d", prefix, next)
		}
		spec := labhost.VMSpec{Name: name, MAC: labhost.MAC(lh.Index, next), CPUs: req.CPUs, MemMiB: req.MemMiB, DiskGiB: req.DiskGiB, DataGiB: req.DataGiB, Kernel: lh.Kernel, Initrd: lh.Initrd, Arch: lh.Capacity.Arch, Bridge: lh.Capacity.Bridge}
		if err := lc.Define(ctx, spec); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		hw, _ := json.Marshal(map[string]any{"manufacturer": "Kubit lab", "product": "KVM VM on " + labHostname(host), "virtual": true, "cpus": req.CPUs, "memoryBytes": int64(req.MemMiB) << 20, "disks": []any{}, "links": []any{}})
		_ = s.store.UpsertNode(ctx, store.NodeRow{MAC: spec.MAC, Hostname: name, Source: "lab", State: "booting", Arch: lh.Capacity.Arch, Hardware: hw})
		_ = s.store.SetMachineHost(ctx, spec.MAC, mac)
		existing = append(existing, labhost.VM{Name: name, MAC: spec.MAC})
		created = append(created, name)
		createdMACs = append(createdMACs, spec.MAC)
		sink(clusterEvent{Time: time.Now(), Kind: "log", Level: "info", Step: "define", Node: name, Message: fmt.Sprintf("defined and started: %d vCPU, %d MiB, %d GiB%s, %s", req.CPUs, req.MemMiB, req.DiskGiB, map[bool]string{true: fmt.Sprintf(" + %d GiB data", req.DataGiB), false: ""}[req.DataGiB > 0], spec.MAC)})
		next++
	}
	lh.VMs, _ = lc.List(ctx)
	_ = s.store.SetLabHost(ctx, mac, lh)
	// Talos in maintenance mode appears on the VMs' leases within a minute or two.
	pending := map[string]bool{}
	for _, n := range created {
		pending[n] = true
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
		for _, vm := range vms {
			if !pending[vm.Name] || vm.IP == "" {
				continue
			}
			res := talos.Probe(ctx, vm.IP, 2*time.Second)
			if res.Err == nil && res.State == talos.StateMaintenance {
				row := rowFromScan(res)
				row.Source = "lab"
				_ = s.store.UpsertNode(ctx, row)
				_ = s.store.SetMachineHost(ctx, vm.MAC, mac)
				delete(pending, vm.Name)
				sink(clusterEvent{Time: time.Now(), Kind: "log", Level: "info", Step: "boot", Node: vm.Name, Message: "Talos maintenance mode at " + vm.IP})
			}
		}
		lh.VMs = vms
		_ = s.store.SetLabHost(ctx, mac, lh)
	}
	if len(pending) > 0 {
		names := make([]string, 0, len(pending))
		for n := range pending {
			names = append(names, n)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("%s did not reach Talos maintenance mode within 6 minutes (check the VM console on the host: virsh console <name>)", strings.Join(names, ", "))
	}
	_ = s.store.Audit(ctx, "", "labhost.vms", fmt.Sprintf("%s +%d", mac, req.Count))
	sink(clusterEvent{Time: time.Now(), Kind: "log", Level: "done", Step: "boot", Message: fmt.Sprintf("%d VM(s) in maintenance mode, ready to be picked for a cluster", req.Count)})
	return createdMACs, nil
}

func usedMem(lh *store.LabHost) int {
	n := 0
	for _, vm := range lh.VMs {
		n += vm.MemMiB
	}
	return n
}

func nameTaken(vms []labhost.VM, name string) bool {
	for _, v := range vms {
		if v.Name == name {
			return true
		}
	}
	return false
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

// vmRow finds the machine row of a VM by libvirt name (hostname column until Talos
// renames it) or MAC.
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
	mac, name, action := strings.ToLower(r.PathValue("mac")), r.PathValue("name"), r.PathValue("action")
	host, err := s.store.GetMachine(r.Context(), mac)
	if err != nil || host.LabHost == nil {
		http.Error(w, "not a lab host", http.StatusNotFound)
		return
	}
	id, err := s.runOperation("", "labhost.vm."+action, map[string]string{"host": mac, "vm": name}, func(ctx contextT, sink clusterSink) (any, error) {
		lc, err := s.manager.LabDial(ctx, host)
		if err != nil {
			return nil, err
		}
		defer lc.Close()
		switch action {
		case "start":
			err = lc.Start(ctx, name)
		case "stop":
			err = lc.Stop(ctx, name, false)
		case "kill":
			err = lc.Stop(ctx, name, true)
		case "reprovision":
			if vm := s.vmRow(ctx, host, name); vm != nil && vm.Cluster != "" {
				return nil, fmt.Errorf("%s is a member of %s; remove it from the cluster first", name, vm.Cluster)
			}
			if err = lc.SetTalosBoot(ctx, name, host.LabHost.Kernel, host.LabHost.Initrd); err == nil {
				_ = lc.Stop(ctx, name, true)
				err = lc.Start(ctx, name)
			}
		default:
			return nil, fmt.Errorf("unknown action %q", action)
		}
		if err != nil {
			return nil, err
		}
		host.LabHost.VMs, _ = lc.List(ctx)
		_ = s.store.SetLabHost(ctx, mac, host.LabHost)
		return nil, nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"operationId": id})
}

func (s *Server) handleLabVMResize(w http.ResponseWriter, r *http.Request) {
	mac, name := strings.ToLower(r.PathValue("mac")), r.PathValue("name")
	var req struct{ CPUs, MemMiB int }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.CPUs < 1 || req.MemMiB < 1024 {
		http.Error(w, `body: {"cpus":2,"memMiB":3072}`, http.StatusBadRequest)
		return
	}
	host, err := s.store.GetMachine(r.Context(), mac)
	if err != nil || host.LabHost == nil {
		http.Error(w, "not a lab host", http.StatusNotFound)
		return
	}
	lc, err := s.manager.LabDial(r.Context(), host)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer lc.Close()
	if err := lc.Resize(r.Context(), name, req.CPUs, req.MemMiB); err != nil {
		writeErr(w, err)
		return
	}
	host.LabHost.VMs, _ = lc.List(r.Context())
	_ = s.store.SetLabHost(r.Context(), mac, host.LabHost)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLabVMDelete(w http.ResponseWriter, r *http.Request) {
	mac, name := strings.ToLower(r.PathValue("mac")), r.PathValue("name")
	host, err := s.store.GetMachine(r.Context(), mac)
	if err != nil || host.LabHost == nil {
		http.Error(w, "not a lab host", http.StatusNotFound)
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
	host.LabHost.VMs, _ = lc.List(r.Context())
	_ = s.store.SetLabHost(r.Context(), mac, host.LabHost)
	_ = s.store.Audit(r.Context(), "", "labhost.vm.delete", mac+" "+name)
	w.WriteHeader(http.StatusNoContent)
}

// handleLabRelease destroys every VM and drops the host role; members block it.
func (s *Server) handleLabRelease(w http.ResponseWriter, r *http.Request) {
	mac := strings.ToLower(r.PathValue("mac"))
	host, err := s.store.GetMachine(r.Context(), mac)
	if err != nil || host.LabHost == nil {
		http.Error(w, "not a lab host", http.StatusNotFound)
		return
	}
	for _, v := range host.LabHost.VMs {
		if vm, err := s.store.GetMachine(r.Context(), v.MAC); err == nil && vm.Cluster != "" {
			http.Error(w, fmt.Sprintf("%s is a member of %s; remove it first", v.Name, vm.Cluster), http.StatusConflict)
			return
		}
	}
	// A host that never finished installing has nothing to clean up; do not hang the
	// request on an SSH timeout for it.
	if host.LabHost.State == "ready" || len(host.LabHost.VMs) > 0 {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		if lc, err := s.manager.LabDial(ctx, host); err == nil {
			for _, v := range host.LabHost.VMs {
				_ = lc.Delete(ctx, v.Name)
				_ = s.store.DeleteMachine(ctx, v.MAC)
			}
			lc.Close()
		}
	}
	_ = s.store.SetLabHost(r.Context(), mac, nil)
	_ = s.store.SetNodeState(r.Context(), host.IP, "configured")
	_ = s.store.Audit(r.Context(), "", "labhost.release", mac)
	w.WriteHeader(http.StatusNoContent)
}

// pxeDecision extension: an armed lab host boots the Debian installer.
func labBoot(m *store.Machine) (string, bool) {
	if m != nil && m.Provision && m.ProvisionKind == "labhost" {
		return "debian", true
	}
	return "", false
}
