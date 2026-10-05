package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/oob"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
)

func (s *Server) oobRoutes() {
	r := s.mux
	r.HandleFunc("PUT /api/v1/machines/{mac}/oob", s.handleOOBSave)
	r.HandleFunc("POST /api/v1/machines/{mac}/oob/test", s.handleOOBTest)
	r.HandleFunc("POST /api/v1/machines/{mac}/power", s.handleOOBPower)
	r.HandleFunc("POST /api/v1/machines/oob", s.handleOOBAdd)
}

func (s *Server) oobConfig(ctx context.Context, mac string, submitted oob.Config) oob.Config {
	if submitted.Password == store.Masked || submitted.Password == "" {
		if cur, err := s.store.MachineOOB(ctx, mac); err == nil {
			submitted.Password = cur.Password
		}
	}
	return submitted
}

func (s *Server) handleOOBSave(w http.ResponseWriter, r *http.Request) {
	mac := pathMAC(r)
	var c oob.Config
	if !decodeJSON(w, r, &c) {
		return
	}
	if c.Type == "" {
		if err := s.store.SetMachineOOB(r.Context(), mac, nil); err != nil {
			writeErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	c = s.oobConfig(r.Context(), mac, c)
	if _, err := oob.Open(c); err != nil {
		writeErr(w, unprocessable(err))
		return
	}
	if err := s.store.SetMachineOOB(r.Context(), mac, &c); err != nil {
		writeErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), "", "machine.oob", mac+" "+c.Type+" "+c.Host)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleOOBTest(w http.ResponseWriter, r *http.Request) {
	mac := pathMAC(r)
	var c oob.Config
	if !decodeOptionalJSON(w, r, &c) {
		return
	}
	if c.Type == "" {
		if cur, err := s.store.MachineOOB(r.Context(), mac); err == nil {
			c = *cur
		}
	}
	c = s.oobConfig(r.Context(), mac, c)
	mgr, err := oob.Open(c)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	info, err := mgr.Probe(ctx)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if info.MAC != "" && info.MAC != mac {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": fmt.Sprintf("%s reports MAC %s, this machine is %s: wrong address?", oob.Label(c.Type), info.MAC, mac), "info": info})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "info": info})
}

func (s *Server) handleOOBAdd(w http.ResponseWriter, r *http.Request) {
	var c oob.Config
	if !decodeJSON(w, r, &c) {
		return
	}
	mgr, err := oob.Open(c)
	if err != nil {
		writeErr(w, unprocessable(err))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	info, err := mgr.Probe(ctx)
	if err != nil {
		writeErr(w, &statusError{Status: http.StatusBadGateway, Msg: err.Error()})
		return
	}
	if info.MAC == "" {
		writeErr(w, &statusError{Status: http.StatusBadGateway, Msg: oob.Label(c.Type) + " did not report a wired MAC address"})
		return
	}
	row := store.NodeRow{MAC: info.MAC, UUID: info.UUID, Serial: info.Serial, Source: c.Type, State: "off"}
	if info.Power == "on" {
		row.State = "unknown"
	}
	if existing, err := s.store.GetMachine(ctx, info.MAC); err == nil {
		row.State = existing.State
	}
	if err := s.store.UpsertNode(ctx, row); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.store.SetMachineOOB(ctx, info.MAC, &c); err != nil {
		writeErr(w, err)
		return
	}
	if info.Model != "" {
		_ = s.store.UpsertNode(ctx, store.NodeRow{MAC: info.MAC, Source: c.Type, Hardware: oobHardware(info)})
	}
	_ = s.store.Audit(ctx, "", "machine.oob.add", info.MAC+" "+c.Host)
	m, err := s.store.GetMachine(ctx, info.MAC)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"machine": machineView(*m), "info": info})
}

func (s *Server) handleOOBPower(w http.ResponseWriter, r *http.Request) {
	mac := pathMAC(r)
	var req struct {
		Action oob.Action `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Action == "" {
		writeErr(w, badRequest(`body must be {"action": "on|off|reset|cycle|pxe"}`))
		return
	}
	m, err := s.store.GetMachine(r.Context(), mac)
	if err != nil {
		writeErr(w, err)
		return
	}
	c, err := s.store.MachineOOB(r.Context(), mac)
	if err != nil {
		writeErr(w, conflict("no remote management configured for this machine"))
		return
	}
	if req.Action == oob.BootPXE && m.Cluster != "" {
		writeErr(w, conflict(fmt.Sprintf("%s is a member of %s; remove it from the cluster first (that resets it to maintenance mode without PXE)", m.Hostname, m.Cluster)))
		return
	}
	if req.Action == oob.BootPXE && m.Kind() == store.KindLabHost {
		writeErr(w, conflict("A lab host boots its own disk; release it first."))
		return
	}
	if req.Action == oob.BootPXE && m.IsLabVM() {
		writeErr(w, conflict("Lab VMs are re-provisioned from their host."))
		return
	}
	if req.Action == oob.BootPXE {
		if _, ok := s.pxeServing(w, r, false); !ok {
			return
		}
	}
	s.startOp(w, m.Cluster, "machine.power", map[string]string{"mac": mac, "action": string(req.Action), "hostname": m.Hostname}, func(ctx context.Context, sink cluster.Sink) (any, error) {
		return s.oobPowerOp(ctx, sink, m, c, req.Action)
	})
}

func (s *Server) oobPowerOp(ctx context.Context, sink cluster.Sink, m *store.Machine, c *oob.Config, action oob.Action) (result any, err error) {
	mac := m.MAC
	if action == oob.BootPXE {
		defer func() {
			if err != nil {
				_ = s.store.SetMachineProvision(context.Background(), mac, false)
			}
		}()
		sink.Plan(cluster.Steps("power", "Arm a network boot and reset via "+oob.Label(c.Type), "boot", "Network boot request seen", "ipxe", "Talos kernel fetched", "wait", "Wait for Talos maintenance mode")...)
	}
	sink.Begin("power")
	mgr, err := oob.Open(*c, oob.WithTrace(func(line string) {
		sink.Emit(cluster.Info, "power", "", "%s: %s", c.Type, line)
	}))
	if err != nil {
		return nil, err
	}
	if action == oob.BootPXE {
		if err := s.store.SetMachineProvision(ctx, mac, true); err != nil {
			return nil, err
		}
		sink.Emit(cluster.Info, "power", "", "armed: Kubit's PXE server hands Talos to %s on its next boot", mac)
	}
	sink.Emit(cluster.Info, "power", "", "%s via %s at %s", action, c.Type, c.Host)
	if err := mgr.Power(ctx, action); err != nil {
		return nil, err
	}
	_ = s.store.Audit(ctx, m.Cluster, "machine.power", mac+" "+string(action))
	sink.End("power")
	if action != oob.BootPXE {
		return nil, nil
	}
	watch := newPXEWatch(s, mac, sink)
	if err := s.labWaitBoot(ctx, watch, labBootBudget(c)); err != nil {
		return nil, err
	}
	sink.Begin("wait")
	candidates := []string{m.IP, c.Host}
	deadline := time.Now().Add(8 * time.Minute)
	for time.Now().Before(deadline) {
		watch.step = "wait"
		watch.poll(ctx)
		for _, ip := range uniq(m.IP, c.Host, watch.ip) {
			if ip == "" {
				continue
			}
			res := talos.Probe(ctx, ip, 2*time.Second)
			if res.Err == nil && res.State == talos.StateMaintenance {
				_ = s.store.UpsertNode(ctx, cluster.RowFromScan(res))
				sink.Emit(cluster.Done, "wait", ip, "Talos %s in maintenance mode at %s; the machine can now be adopted or used in a new cluster", res.Inventory.TalosVersion, ip)
				sink.End("wait")
				return nil, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	return nil, fmt.Errorf("no Talos maintenance mode at %s within 8 minutes: is kubit pxe running on this LAN, and did the machine network-boot (Network boot page shows its MAC)?", strings.Join(candidates, " / "))
}

func oobHardware(info oob.Info) []byte {
	inv := talos.Inventory{Manufacturer: info.Manufacturer, Product: info.Model, Serial: info.Serial, UUID: info.UUID, CPUs: info.CPUs, MemoryBytes: uint64(max(info.MemoryBytes, 0)), Disks: make([]talos.Disk, 0, len(info.Disks))}
	for _, d := range info.Disks {
		inv.Disks = append(inv.Disks, talos.Disk{Model: d.Model, SizeBytes: uint64(max(d.SizeBytes, 0)), Transport: d.Transport, Rotational: d.Media == "hdd"})
	}
	return placeholderHardware(inv)
}
