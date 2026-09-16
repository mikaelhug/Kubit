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

// handleOOBPower runs a power action as an operation; "talos" boots the machine
// from the Talos ISO over IDE-R and waits for maintenance mode.
func (s *Server) handleOOBPower(w http.ResponseWriter, r *http.Request) {
	mac := strings.ToLower(r.PathValue("mac"))
	var req struct {
		Action oob.Action `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Action == "" {
		http.Error(w, `body must be {"action": "on|off|reset|cycle|talos"}`, http.StatusBadRequest)
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
	if req.Action == oob.BootTalos && m.Cluster != "" {
		http.Error(w, fmt.Sprintf("%s is a member of %s; remove it from the cluster first (that resets it to maintenance mode)", m.Hostname, m.Cluster), http.StatusConflict)
		return
	}
	id, err := s.runOperation(m.Cluster, "machine.power", map[string]string{"mac": mac, "action": string(req.Action), "hostname": m.Hostname}, func(ctx contextT, sink clusterSink) (result any, err error) {
		if req.Action == oob.BootTalos {
			return nil, s.bootTalos(ctx, m, c, sink)
		}
		sink(clusterEvent{Time: time.Now(), Kind: "step", Step: "power", Status: cluster.StepRunning})
		mgr, err := oob.Open(*c)
		if err != nil {
			return nil, err
		}
		sink(clusterEvent{Time: time.Now(), Kind: "log", Level: "info", Step: "power", Message: fmt.Sprintf("%s via %s at %s", req.Action, c.Type, c.Host)})
		if err := mgr.Power(ctx, req.Action); err != nil {
			return nil, err
		}
		_ = s.store.Audit(ctx, m.Cluster, "machine.power", mac+" "+string(req.Action))
		sink(clusterEvent{Time: time.Now(), Kind: "step", Step: "power", Status: cluster.StepDone})
		return nil, nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"operationId": id})
}

// bootTalos attaches the Talos ISO over IDE-R, boots from it and waits for
// maintenance mode; the session ends once Talos answers (it runs from RAM).
func (s *Server) bootTalos(ctx contextT, m *store.Machine, c *oob.Config, sink clusterSink) error {
	mac := m.MAC
	sink(clusterEvent{Time: time.Now(), Kind: "steps", Level: "info", Steps: cluster.Steps("prepare", "Fetch the Talos ISO", "media", "Attach the CD over AMT", "power", "Boot from it", "boot", "Firmware reads the CD", "wait", "Wait for Talos maintenance mode")})
	sink(clusterEvent{Time: time.Now(), Kind: "step", Step: "prepare", Status: cluster.StepRunning})
	arch := m.Arch
	if arch == "" {
		arch = "amd64"
	}
	version := s.latestStableTalos(ctx)
	if version == "" {
		version = defaultTalosVersion()
	}
	schematic, err := s.manager.Factory.CreateSchematic(ctx, []string{"siderolabs/gvisor"})
	if err != nil {
		return err
	}
	iso, err := s.media.TalosISO(ctx, s.manager.Factory.BaseURL, schematic, version, arch)
	if err != nil {
		return fmt.Errorf("Talos ISO: %w", err)
	}
	sink(clusterEvent{Time: time.Now(), Kind: "log", Level: "info", Step: "prepare", Message: fmt.Sprintf("Talos %s %s ISO ready", version, arch)})
	sink(clusterEvent{Time: time.Now(), Kind: "step", Step: "prepare", Status: cluster.StepDone})
	b, err := s.bootFromMedia(ctx, m, c, iso, sink)
	if err != nil {
		return err
	}
	defer b.stop()
	_ = s.store.Audit(ctx, "", "machine.power", mac+" talos")
	candidates := uniq(m.IP, c.Host)
	return phase(ctx, sink, b, mac, "wait", labLoadWait, func() (bool, string) {
		for _, ip := range candidates {
			if ip == "" {
				continue
			}
			res := talos.Probe(ctx, ip, 2*time.Second)
			if res.Err == nil && res.State == talos.StateMaintenance {
				_ = s.store.UpsertNode(ctx, rowFromScan(res))
				return true, fmt.Sprintf("Talos %s in maintenance mode at %s; the machine can now be adopted or used in a new cluster", res.Inventory.TalosVersion, ip)
			}
		}
		if time.Since(b.lastRead) > labLoadStall {
			return false, fmt.Sprintf("the firmware read %.1f MB then stopped and Talos never answered at %s: the image did not boot (Secure Boot enabled?) or Talos came up on another address — check the Inventory scan", float64(b.bytes)/1e6, strings.Join(candidates, " / "))
		}
		return false, fmt.Sprintf("Talos did not answer at %s within %s", strings.Join(candidates, " / "), labLoadWait)
	})
}

