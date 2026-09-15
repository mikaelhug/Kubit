package api

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/mikael/kubit/internal/config"
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
	v := nodeView{NodeRow: *m}
	if len(m.Hardware) > 2 {
		var inv talos.Inventory
		if json.Unmarshal(m.Hardware, &inv) == nil {
			v.Inventory = &inv
		}
	}
	v.Hardware = nil
	writeJSON(w, http.StatusOK, v)
}

// handleMachineRetire forgets a machine; cluster members must be removed first.
func (s *Server) handleMachineRetire(w http.ResponseWriter, r *http.Request) {
	m, err := s.store.GetMachine(r.Context(), r.PathValue("mac"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if m.Cluster != "" {
		http.Error(w, fmt.Sprintf("%s is a member of cluster %s; remove it from the cluster first", m.Hostname, m.Cluster), http.StatusConflict)
		return
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
		Enabled bool `json:"enabled"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if err := s.store.SetMachineWOL(r.Context(), r.PathValue("mac"), req.Enabled); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleMachineWake broadcasts a Wake-on-LAN magic packet for the machine.
func (s *Server) handleMachineWake(w http.ResponseWriter, r *http.Request) {
	m, err := s.store.GetMachine(r.Context(), r.PathValue("mac"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if !m.WOL {
		http.Error(w, "Wake-on-LAN is not enabled for this machine", http.StatusConflict)
		return
	}
	if err := WakeOnLAN(m.MAC); err != nil {
		writeErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), m.Cluster, "machine.wake", m.MAC)
	w.WriteHeader(http.StatusAccepted)
}

// WakeOnLAN sends the standard magic packet (6×0xFF + 16×MAC) to the broadcast address
// on UDP 9. Only useful when the daemon shares a segment with the machine.
func WakeOnLAN(mac string) error {
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

// handleDesign proposes a declaration for the selected machines.
func (s *Server) handleDesign(w http.ResponseWriter, r *http.Request) {
	var req designRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	if req.Name == "" {
		req.Name = "cluster"
	}
	machines, err := s.designMachines(r, req.MACs)
	if err != nil {
		writeErr(w, err)
		return
	}
	c, warnings := config.Design(req.Name, machines, config.DesignOptions{MetalLBRange: req.MetalLBRange})
	if warnings == nil {
		warnings = []config.Warning{}
	}
	out, _ := c.Marshal()
	writeJSON(w, http.StatusOK, designResponse{YAML: string(out), Cluster: c, Warnings: warnings, Topology: config.Recommend(len(machines)), Overlaps: s.rangeOverlaps(r, c)})
}

// handleLint lints a declaration (YAML body) against the known machines.
func (s *Server) handleLint(w http.ResponseWriter, r *http.Request) {
	var req struct {
		YAML string `json:"yaml"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	c, err := config.Parse([]byte(req.YAML))
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	var macs []string
	for _, n := range c.Spec.Nodes {
		if n.MAC != "" {
			macs = append(macs, n.MAC)
		}
	}
	machines, _ := s.designMachines(r, macs)
	warnings := config.Lint(c, machines)
	for _, o := range s.rangeOverlaps(r, c) {
		warnings = append(warnings, config.Warning{Level: "warn", Code: "metallb-overlap", Message: fmt.Sprintf("MetalLB range overlaps cluster %s's range on the same LAN.", o)})
	}
	for _, o := range s.vipConflicts(r, c) {
		warnings = append(warnings, config.Warning{Level: "warn", Code: "vip-taken", Message: fmt.Sprintf("VIP %s is already cluster %s's VIP.", c.Spec.ControlPlane.VIP, o)})
	}
	if warnings == nil {
		warnings = []config.Warning{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"warnings": warnings, "yaml": mustYAML(c)})
}

func (s *Server) designMachines(r *http.Request, macs []string) ([]config.Machine, error) {
	var out []config.Machine
	for _, mac := range macs {
		m, err := s.store.GetMachine(r.Context(), mac)
		if err != nil {
			return nil, err
		}
		var inv talos.Inventory
		_ = json.Unmarshal(m.Hardware, &inv)
		cm := config.Machine{IP: m.IP, MAC: m.MAC, UUID: m.UUID, Arch: config.Arch(m.Arch), CPUs: inv.CPUs, MemBytes: inv.MemoryBytes, KVM: inv.KVM, Virtual: inv.Virtual || talos.IsVirtual(inv.Manufacturer, inv.Product), Model: strings.TrimSpace(inv.Manufacturer + " " + inv.Product)}
		if cm.Arch == "" {
			cm.Arch = config.ArchAMD64
		}
		for _, d := range inv.InstallCandidates() {
			cm.Disks = append(cm.Disks, config.MachineDisk{DevPath: d.DevPath, SizeBytes: d.SizeBytes, Transport: d.Transport})
		}
		out = append(out, cm)
	}
	return out, nil
}

func (s *Server) rangeOverlaps(r *http.Request, c *config.Cluster) []string {
	if !c.Spec.Platform.MetalLB.Enabled {
		return nil
	}
	rows, err := s.store.ListClusters(r.Context())
	if err != nil {
		return nil
	}
	others := map[string]string{}
	for _, row := range rows {
		if row.Name == c.Metadata.Name {
			continue
		}
		if oc, err := config.Parse(row.Spec); err == nil && oc.Spec.Platform.MetalLB.Enabled {
			others[row.Name] = oc.Spec.Platform.MetalLB.Range
		}
	}
	return config.Overlaps(c.Spec.Platform.MetalLB.Range, others)
}

// vipConflicts lists stored clusters that use the same control-plane VIP.
func (s *Server) vipConflicts(r *http.Request, c *config.Cluster) []string {
	if c.Spec.ControlPlane.VIP == "" {
		return nil
	}
	rows, err := s.store.ListClusters(r.Context())
	if err != nil {
		return nil
	}
	var out []string
	for _, row := range rows {
		if row.Name == c.Metadata.Name {
			continue
		}
		if oc, err := config.Parse(row.Spec); err == nil && oc.Spec.ControlPlane.VIP == c.Spec.ControlPlane.VIP {
			out = append(out, row.Name)
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
		http.Error(w, `body must be {"to": "<new-hostname>"}`, http.StatusBadRequest)
		return
	}
	id, err := s.runOperation(name, "node.rename", map[string]string{"hostname": hostname, "to": req.To}, func(ctx contextT, sink clusterSink) (any, error) {
		return nil, s.manager.RenameNode(ctx, name, hostname, req.To, sink)
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"operationId": id})
}

func (s *Server) handleNodePool(w http.ResponseWriter, r *http.Request) {
	name, hostname := r.PathValue("name"), r.PathValue("hostname")
	var req struct {
		Pool string `json:"pool"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Pool == "" {
		http.Error(w, `body must be {"pool": "<pool>"}`, http.StatusBadRequest)
		return
	}
	id, err := s.runOperation(name, "node.pool", map[string]string{"hostname": hostname, "pool": req.Pool}, func(ctx contextT, sink clusterSink) (any, error) {
		return nil, s.manager.MoveNodeToPool(ctx, name, hostname, req.Pool, sink)
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"operationId": id})
}

func (s *Server) handleNodeReaddress(w http.ResponseWriter, r *http.Request) {
	name, hostname := r.PathValue("name"), r.PathValue("hostname")
	var req struct {
		Network *config.NodeNetwork `json:"network"` // null = back to DHCP
		IP      string              `json:"ip"`      // the address Kubit should use afterwards
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	if req.IP == "" && req.Network != nil && len(req.Network.Addresses) > 0 {
		if pfx, err := parsePrefix(req.Network.Addresses[0]); err == nil {
			req.IP = pfx
		}
	}
	id, err := s.runOperation(name, "node.readdress", req, func(ctx contextT, sink clusterSink) (any, error) {
		return nil, s.manager.ReaddressNode(ctx, name, hostname, req.Network, req.IP, sink)
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"operationId": id})
}

func parsePrefix(cidr string) (string, error) {
	ip, _, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", err
	}
	return ip.String(), nil
}

// handlePoolsSave replaces the pool list of a cluster (nodes keep their pool names).
func (s *Server) handlePoolsSave(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var pools []config.Pool
	if err := json.NewDecoder(r.Body).Decode(&pools); err != nil {
		writeErr(w, err)
		return
	}
	c, row, err := s.manager.LoadCluster(r.Context(), name)
	if err != nil {
		writeErr(w, err)
		return
	}
	c.Spec.Pools = pools
	// Re-run defaults so removed pools do not leave nodes dangling; Validate then reports them.
	if err := c.Validate(); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	if err := s.manager.EnsureSchematic(r.Context(), c); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.manager.SaveCluster(r.Context(), c, row.State); err != nil {
		writeErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), name, "pools.save", "")
	writeJSON(w, http.StatusOK, c.Spec.Pools)
}
