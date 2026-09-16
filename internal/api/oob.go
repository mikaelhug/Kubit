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
	r.HandleFunc("GET /api/v1/pxe/decide", s.handlePXEDecide)
}

// oobConfig merges a submitted config with the stored one (a masked password keeps
// the stored secret).
func (s *Server) oobConfig(ctx context.Context, mac string, submitted oob.Config) oob.Config {
	if submitted.Password == "•••" || submitted.Password == "" {
		if cur, err := s.store.MachineOOB(ctx, mac); err == nil {
			submitted.Password = cur.Password
		}
	}
	return submitted
}

func (s *Server) handleOOBSave(w http.ResponseWriter, r *http.Request) {
	mac := strings.ToLower(r.PathValue("mac"))
	var c oob.Config
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeErr(w, err)
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
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	if err := s.store.SetMachineOOB(r.Context(), mac, &c); err != nil {
		writeErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), "", "machine.oob", mac+" "+c.Type+" "+c.Host)
	w.WriteHeader(http.StatusNoContent)
}

// handleOOBTest probes with the submitted (or stored) config and records what the
// management engine reports about the machine.
func (s *Server) handleOOBTest(w http.ResponseWriter, r *http.Request) {
	mac := strings.ToLower(r.PathValue("mac"))
	var c oob.Config
	_ = json.NewDecoder(r.Body).Decode(&c)
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
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": fmt.Sprintf("AMT reports MAC %s, this machine is %s: wrong address?", info.MAC, mac), "info": info})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "info": info})
}

// handleOOBAdd creates a machine from its management engine alone: AMT tells us the
// MAC, model and serial before Talos ever booted, so the machine can be booted into
// Talos from the Inventory.
func (s *Server) handleOOBAdd(w http.ResponseWriter, r *http.Request) {
	var c oob.Config
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeErr(w, err)
		return
	}
	mgr, err := oob.Open(c)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	info, err := mgr.Probe(ctx)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	if info.MAC == "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "AMT did not report a wired MAC address"})
		return
	}
	row := store.NodeRow{MAC: info.MAC, Serial: info.Serial, Source: "amt", State: "off"}
	if info.Power == "on" {
		row.State = "unknown"
	}
	if existing, err := s.store.GetMachine(ctx, info.MAC); err == nil {
		row.State = existing.State // already discovered: keep what Talos said
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
		// Hardware normally comes from Talos; until then the chassis strings stand in.
		hw, _ := json.Marshal(map[string]any{"manufacturer": info.Manufacturer, "product": info.Model, "serial": info.Serial, "disks": []any{}, "links": []any{}})
		_ = s.store.UpsertNode(ctx, store.NodeRow{MAC: info.MAC, Source: "amt", Hardware: hw})
	}
	_ = s.store.Audit(ctx, "", "machine.oob.add", info.MAC+" "+c.Host)
	m, _ := s.store.GetMachine(ctx, info.MAC)
	writeJSON(w, http.StatusCreated, map[string]any{"machine": machineView(*m), "info": info})
}

// handleOOBPower runs a power action as an operation; "pxe" also arms the one-shot
// Talos hand-off so Kubit's PXE server serves Talos to this MAC once.
func (s *Server) handleOOBPower(w http.ResponseWriter, r *http.Request) {
	mac := strings.ToLower(r.PathValue("mac"))
	var req struct {
		Action oob.Action `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Action == "" {
		http.Error(w, `body must be {"action": "on|off|reset|cycle|pxe"}`, http.StatusBadRequest)
		return
	}
	m, err := s.store.GetMachine(r.Context(), mac)
	if err != nil {
		writeErr(w, err)
		return
	}
	c, err := s.store.MachineOOB(r.Context(), mac)
	if err != nil {
		http.Error(w, "no remote management configured for this machine", http.StatusConflict)
		return
	}
	if req.Action == oob.BootPXE && !s.pxeRunning(r.Context()) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "The PXE server is not running, so the machine would find nothing to boot. Start it in a terminal (it can stay open): " + pxeCommand(r.Host), "code": "pxe-down", "command": pxeCommand(r.Host)})
		return
	}
	if req.Action == oob.BootPXE && m.Cluster != "" {
		http.Error(w, fmt.Sprintf("%s is a member of %s; remove it from the cluster first (that resets it to maintenance mode without PXE)", m.Hostname, m.Cluster), http.StatusConflict)
		return
	}
	id, err := s.runOperation(m.Cluster, "machine.power", map[string]string{"mac": mac, "action": string(req.Action), "hostname": m.Hostname}, func(ctx contextT, sink clusterSink) (result any, err error) {
		if req.Action == oob.BootPXE {
			// Leave nothing armed behind a failed or cancelled attempt.
			defer func() {
				if err != nil {
					_ = s.store.SetMachineProvision(context.Background(), mac, false)
				}
			}()
			sink(clusterEvent{Time: time.Now(), Kind: "steps", Level: "info", Steps: cluster.Steps("power", "Arm a network boot and reset via AMT", "boot", "Network boot request seen", "ipxe", "Talos kernel fetched", "wait", "Wait for Talos maintenance mode")})
		}
		sink(clusterEvent{Time: time.Now(), Kind: "step", Step: "power", Status: cluster.StepRunning})
		mgr, err := oob.Open(*c)
		if err != nil {
			return nil, err
		}
		if req.Action == oob.BootPXE {
			if err := s.store.SetMachineProvision(ctx, mac, true); err != nil {
				return nil, err
			}
			sink(clusterEvent{Time: time.Now(), Kind: "log", Level: "info", Step: "power", Message: "armed: Kubit's PXE server hands Talos to " + mac + " on its next boot"})
		}
		sink(clusterEvent{Time: time.Now(), Kind: "log", Level: "info", Step: "power", Message: fmt.Sprintf("%s via %s at %s", req.Action, c.Type, c.Host)})
		if err := mgr.Power(ctx, req.Action); err != nil {
			return nil, err
		}
		_ = s.store.Audit(ctx, m.Cluster, "machine.power", mac+" "+string(req.Action))
		sink(clusterEvent{Time: time.Now(), Kind: "step", Step: "power", Status: cluster.StepDone})
		if req.Action != oob.BootPXE {
			return nil, nil
		}
		// The PXE server's view first: whether the box asked to network-boot at all
		// and whether it fetched the kernel, so a BIOS or LAN problem is named in
		// minutes, not after the maintenance-mode timeout.
		watch := &pxeWatch{s: s, mac: mac, sink: sink}
		if err := s.labWaitBoot(ctx, watch); err != nil {
			return nil, err
		}
		// Talos normally comes up on the same lease (same MAC); probe that address and
		// the AMT address until maintenance mode answers, then record it like a scan.
		sink(clusterEvent{Time: time.Now(), Kind: "step", Step: "wait", Status: cluster.StepRunning})
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
					_ = s.store.UpsertNode(ctx, rowFromScan(res))
					sink(clusterEvent{Time: time.Now(), Kind: "log", Level: "done", Step: "wait", Node: ip, Message: fmt.Sprintf("Talos %s in maintenance mode at %s; the machine can now be adopted or used in a new cluster", res.Inventory.TalosVersion, ip)})
					sink(clusterEvent{Time: time.Now(), Kind: "step", Step: "wait", Status: cluster.StepDone})
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
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"operationId": id})
}

// handlePXEDecide is what the PXE process asks for every boot request: hand this
// MAC Talos (maintenance mode) or let it boot from its own disk.
func (s *Server) handlePXEDecide(w http.ResponseWriter, r *http.Request) {
	mac := strings.ToLower(r.URL.Query().Get("mac"))
	boot, reason := s.pxeDecision(r.Context(), mac)
	writeJSON(w, http.StatusOK, map[string]string{"boot": boot, "reason": reason})
}

func (s *Server) pxeDecision(ctx context.Context, mac string) (string, string) {
	m, err := s.store.GetMachine(ctx, mac)
	if err != nil {
		v, _ := s.store.GetSettings(ctx)
		if v.PXEEnrollment == "closed" {
			return "local", "unknown machine and enrollment is closed"
		}
		return "talos", "unknown machine, enrollment open"
	}
	if boot, ok := labBoot(m); ok {
		return boot, "armed as lab host: Debian installer"
	}
	switch {
	case m.Provision:
		return "talos", "armed with Boot into Talos"
	case m.Cluster != "":
		return "local", "member of cluster " + m.Cluster
	default:
		return "talos", "known, unassigned machine"
	}
}
