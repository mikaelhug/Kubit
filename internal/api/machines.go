package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/labhost"
	"github.com/mikael/kubit/internal/netx"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
)

func (s *Server) machineRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/machines", s.handleNodes)
	r.HandleFunc("GET /api/v1/machines/{mac}", s.handleMachine)
	r.HandleFunc("DELETE /api/v1/machines/{mac}", s.handleMachineRetire)
	r.HandleFunc("PUT /api/v1/machines/{mac}/wol", s.handleMachineWOL)
	r.HandleFunc("POST /api/v1/machines/{mac}/wake", s.handleMachineWake)
	r.HandleFunc("POST /api/v1/config/design", s.handleDesign)
	r.HandleFunc("POST /api/v1/config/lint", s.handleLint)
	r.HandleFunc("POST /api/v1/clusters/{name}/nodes/{hostname}/rename", s.disruptive(s.handleNodeRename))
	r.HandleFunc("POST /api/v1/clusters/{name}/nodes/{hostname}/pool", s.disruptive(s.handleNodePool))
	r.HandleFunc("POST /api/v1/clusters/{name}/nodes/{hostname}/readdress", s.disruptive(s.handleNodeReaddress))
	r.HandleFunc("PUT /api/v1/clusters/{name}/pools", s.handlePoolsSave)
}

func (s *Server) handleMachine(w http.ResponseWriter, r *http.Request) {
	m, err := s.store.GetMachine(r.Context(), r.PathValue("mac"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, machineView(*m))
}

func (s *Server) handleMachineRetire(w http.ResponseWriter, r *http.Request) {
	m, err := s.store.GetMachine(r.Context(), r.PathValue("mac"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if m.Cluster != "" {
		writeErr(w, conflict(fmt.Sprintf("%s is a member of cluster %s; remove it from the cluster first", m.Hostname, m.Cluster)))
		return
	}
	if m.LabHost != nil {
		writeErr(w, conflict("Release the lab host first."))
		return
	}
	if m.IsLabVM() {
		if host, err := s.store.GetMachine(r.Context(), m.Host); err == nil && host.LabHost != nil && slices.ContainsFunc(host.LabHost.VMs, func(v labhost.VM) bool { return netx.MACKey(v.MAC) == netx.MACKey(m.MAC) }) {
			writeErr(w, conflict("Delete the VM from its lab host instead."))
			return
		}
	}
	if err := s.store.DeleteMachine(r.Context(), m.MAC); err != nil {
		writeErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), "", "machine.retire", m.MAC)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMachineWOL(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Enabled == nil {
		writeErr(w, badRequest(`body must be {"enabled": true|false}`))
		return
	}
	if err := s.store.SetMachineWOL(r.Context(), r.PathValue("mac"), *req.Enabled); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMachineWake(w http.ResponseWriter, r *http.Request) {
	m, err := s.store.GetMachine(r.Context(), r.PathValue("mac"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if !m.WOL {
		writeErr(w, conflict("Wake-on-LAN is not enabled for this machine"))
		return
	}
	if err := wakeOnLAN(m.MAC); err != nil {
		writeErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), m.Cluster, "machine.wake", m.MAC)
	w.WriteHeader(http.StatusAccepted)
}

func wakeOnLAN(mac string) error {
	hw, err := net.ParseMAC(mac)
	if err != nil || len(hw) != 6 {
		return fmt.Errorf("not a 48-bit MAC: %q", mac)
	}
	pkt := make([]byte, 0, 102)
	for i := 0; i < 6; i++ {
		pkt = append(pkt, 0xFF)
	}
	for i := 0; i < 16; i++ {
		pkt = append(pkt, hw...)
	}
	conn, err := net.Dial("udp4", "255.255.255.255:9")
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.Write(pkt)
	return err
}

type designRequest struct {
	Name         string   `json:"name"`
	MACs         []string `json:"macs"`
	MetalLBRange string   `json:"metallbRange,omitempty"`
}

type designResponse struct {
	YAML     string           `json:"yaml"`
	Cluster  *config.Cluster  `json:"cluster"`
	Warnings []config.Warning `json:"warnings"`
	Topology config.Topology  `json:"topology"`
	Overlaps []string         `json:"overlaps,omitempty"`
}

func (s *Server) handleDesign(w http.ResponseWriter, r *http.Request) {
	var req designRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		req.Name = "cluster"
	}
	machines, err := s.designMachines(r.Context(), req.MACs, false)
	if err != nil {
		writeErr(w, err)
		return
	}
	c, warnings := config.Design(req.Name, machines, config.DesignOptions{MetalLBRange: req.MetalLBRange})
	if warnings == nil {
		warnings = []config.Warning{}
	}
	writeJSON(w, http.StatusOK, designResponse{YAML: mustYAML(c), Cluster: c, Warnings: warnings, Topology: config.Recommend(len(machines)), Overlaps: s.rangeOverlapsFor(r, c)})
}

func (s *Server) handleLint(w http.ResponseWriter, r *http.Request) {
	var req struct {
		YAML string `json:"yaml"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	c, err := config.Parse([]byte(req.YAML))
	if err != nil {
		writeErr(w, unprocessable(err))
		return
	}
	var macs []string
	for _, n := range c.Spec.Nodes {
		if n.MAC != "" {
			macs = append(macs, n.MAC)
		}
	}
	machines, _ := s.designMachines(r.Context(), macs, false)
	warnings := config.Lint(c, machines)
	var peers []peerCluster
	if c.Spec.Platform.MetalLB.Enabled || c.Spec.ControlPlane.VIP != "" {
		peers = s.peerClusters(r, c)
	}
	for _, o := range rangeOverlaps(c, peers) {
		warnings = append(warnings, config.Warning{Level: "warn", Code: "metallb-overlap", Message: fmt.Sprintf("MetalLB range overlaps cluster %s's range on the same LAN.", o)})
	}
	for _, o := range vipConflicts(c, peers) {
		warnings = append(warnings, config.Warning{Level: "warn", Code: "vip-taken", Message: fmt.Sprintf("VIP %s is already cluster %s's VIP.", c.Spec.ControlPlane.VIP, o)})
	}
	if warnings == nil {
		warnings = []config.Warning{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"warnings": warnings, "yaml": mustYAML(c)})
}

func (s *Server) designMachines(ctx context.Context, macs []string, labVMs bool) ([]config.Machine, error) {
	var out []config.Machine
	for _, mac := range macs {
		m, err := s.store.GetMachine(ctx, mac)
		if err != nil {
			return nil, err
		}
		out = append(out, designMachine(m, labVMs))
	}
	return out, nil
}

func designMachine(m *store.Machine, labVM bool) config.Machine {
	inv, _ := inventoryOf(m)
	cm := config.Machine{IP: m.IP, MAC: m.MAC, UUID: m.UUID, Arch: config.Arch(m.Arch), CPUs: inv.CPUs, MemBytes: inv.MemoryBytes, KVM: inv.KVM, Virtual: inv.Virtual || talos.IsVirtual(inv.Manufacturer, inv.Product), Host: m.Host, Model: strings.TrimSpace(inv.Manufacturer + " " + inv.Product)}
	if labVM {
		cm.Virtual, cm.Model = true, "Kubit lab VM"
	}
	if cm.Arch == "" {
		cm.Arch = config.ArchAMD64
	}
	cm.Disks = designDisks(inv, labVM || m.Host != "")
	return cm
}

func inventoryOf(m *store.Machine) (talos.Inventory, bool) {
	var inv talos.Inventory
	if len(m.Hardware) <= 2 {
		return inv, false
	}
	return inv, json.Unmarshal(m.Hardware, &inv) == nil
}

type hostnamer struct {
	name         string
	cps, workers int
}

func (h *hostnamer) assign(n *config.Node, controlPlane bool) {
	if controlPlane {
		h.cps++
		n.Role, n.Hostname = config.RoleControlPlane, fmt.Sprintf("%s-cp-%02d", h.name, h.cps)
		return
	}
	h.workers++
	n.Role, n.Hostname = config.RoleWorker, fmt.Sprintf("%s-worker-%02d", h.name, h.workers)
}

func reparse(c *config.Cluster) (*config.Cluster, error) {
	b, err := c.Marshal()
	if err != nil {
		return nil, err
	}
	return config.Parse(b)
}

type peerCluster struct {
	name string
	spec *config.Cluster
}

func (s *Server) peerClusters(r *http.Request, c *config.Cluster) []peerCluster {
	rows, err := s.store.ListClusters(r.Context())
	if err != nil {
		return nil
	}
	var out []peerCluster
	for _, row := range rows {
		if row.Name == c.Metadata.Name {
			continue
		}
		if oc, err := config.Parse(row.Spec); err == nil {
			out = append(out, peerCluster{name: row.Name, spec: oc})
		}
	}
	return out
}

func (s *Server) rangeOverlapsFor(r *http.Request, c *config.Cluster) []string {
	if !c.Spec.Platform.MetalLB.Enabled {
		return nil
	}
	return rangeOverlaps(c, s.peerClusters(r, c))
}

func rangeOverlaps(c *config.Cluster, peers []peerCluster) []string {
	if !c.Spec.Platform.MetalLB.Enabled {
		return nil
	}
	others := map[string]string{}
	for _, p := range peers {
		if p.spec.Spec.Platform.MetalLB.Enabled {
			others[p.name] = p.spec.Spec.Platform.MetalLB.Range
		}
	}
	return config.Overlaps(c.Spec.Platform.MetalLB.Range, others)
}

func vipConflicts(c *config.Cluster, peers []peerCluster) []string {
	if c.Spec.ControlPlane.VIP == "" {
		return nil
	}
	var out []string
	for _, p := range peers {
		if p.spec.Spec.ControlPlane.VIP == c.Spec.ControlPlane.VIP {
			out = append(out, p.name)
		}
	}
	return out
}

func mustYAML(c *config.Cluster) string {
	b, _ := c.Marshal()
	return string(b)
}

func (s *Server) handleNodeRename(w http.ResponseWriter, r *http.Request) {
	name, hostname := r.PathValue("name"), r.PathValue("hostname")
	var req struct {
		To string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.To == "" {
		writeErr(w, badRequest(`body must be {"to": "<new-hostname>"}`))
		return
	}
	s.startOp(w, name, "node.rename", map[string]string{"hostname": hostname, "to": req.To}, func(ctx context.Context, sink cluster.Sink) (any, error) {
		return nil, s.manager.RenameNode(ctx, name, hostname, req.To, sink)
	})
}

func (s *Server) handleNodePool(w http.ResponseWriter, r *http.Request) {
	name, hostname := r.PathValue("name"), r.PathValue("hostname")
	var req struct {
		Pool string `json:"pool"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Pool == "" {
		writeErr(w, badRequest(`body must be {"pool": "<pool>"}`))
		return
	}
	s.startOp(w, name, "node.pool", map[string]string{"hostname": hostname, "pool": req.Pool}, func(ctx context.Context, sink cluster.Sink) (any, error) {
		return nil, s.manager.MoveNodeToPool(ctx, name, hostname, req.Pool, sink)
	})
}

func (s *Server) handleNodeReaddress(w http.ResponseWriter, r *http.Request) {
	name, hostname := r.PathValue("name"), r.PathValue("hostname")
	var req struct {
		Network *config.NodeNetwork `json:"network"`
		IP      string              `json:"ip"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.IP == "" && req.Network != nil && len(req.Network.Addresses) > 0 {
		if pfx, err := parsePrefix(req.Network.Addresses[0]); err == nil {
			req.IP = pfx
		}
	}
	s.startOp(w, name, "node.readdress", req, func(ctx context.Context, sink cluster.Sink) (any, error) {
		return nil, s.manager.ReaddressNode(ctx, name, hostname, req.Network, req.IP, sink)
	})
}

func parsePrefix(cidr string) (string, error) {
	ip, _, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", err
	}
	return ip.String(), nil
}

func (s *Server) handlePoolsSave(w http.ResponseWriter, r *http.Request) {
	var pools []config.Pool
	if !decodeJSON(w, r, &pools) {
		return
	}
	c, ok := s.editCluster(w, r, "pools.save", "", func(c *config.Cluster) error {
		c.Spec.Pools = pools
		if err := c.Validate(); err != nil {
			return unprocessable(err)
		}
		return s.manager.EnsureSchematic(r.Context(), c)
	})
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, c.Spec.Pools)
}
