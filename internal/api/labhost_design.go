package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/labhost"
	"github.com/mikael/kubit/internal/talos"
)

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
