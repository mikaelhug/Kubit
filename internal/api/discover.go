package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/oob"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
)

func (s *Server) discoverRoutes() {
	r := s.mux
	r.HandleFunc("POST /api/v1/discover", s.handleDiscover)
}

type discoverRequest struct {
	Targets []string `json:"targets"`
}

func (s *Server) handleDiscover(w http.ResponseWriter, r *http.Request) {
	var req discoverRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Targets) == 0 {
		writeErr(w, badRequest(`body must be {"targets": ["cidr or ip", ...]}`))
		return
	}
	id, err := s.startDiscover(req.Targets)
	accepted(w, id, err)
}

func (s *Server) startDiscover(targets []string) (int64, error) {
	addrs, err := talos.ExpandTargets(targets)
	if err != nil {
		return 0, invalid(err)
	}
	return s.runOperation("", "discover", discoverRequest{Targets: targets}, func(ctx context.Context, sink cluster.Sink) (any, error) {
		sink.Plan(cluster.Steps("scan", fmt.Sprintf("Probe %d addresses on port 50000", len(addrs)), "record", "Record inventory", "amt", "Probe the rest for Intel AMT or a Redfish BMC")...)
		sink.Begin("scan")
		results := talos.Scan(ctx, addrs, 64, 2*time.Second)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		sink.End("scan")
		sink.Begin("record")
		found, err := cluster.RecordScan(ctx, s.store, results, func(res talos.ScanResult, vipOf string) {
			if vipOf != "" {
				sink.Emit(cluster.Info, "record", res.IP, "VIP of cluster %s, skipped", vipOf)
			} else {
				sink.Emit(cluster.Info, "record", res.IP, "%s", res.State)
			}
		})
		if err != nil {
			return nil, err
		}
		sink.Emit(cluster.Done, "record", "", "%d Talos nodes found", found)
		amtFound := s.discoverOOB(ctx, addrs, results, sink)
		return map[string]int{"found": found, "amt": amtFound}, nil
	})
}

func (s *Server) discoverOOB(ctx context.Context, addrs []netip.Addr, talosResults []talos.ScanResult, sink cluster.Sink) int {
	sink.Begin("amt")
	isTalos := map[string]bool{}
	for _, r := range talosResults {
		if r.Err == nil {
			isTalos[r.IP] = true
		}
	}
	var rest []netip.Addr
	for _, a := range addrs {
		if !isTalos[a.String()] {
			rest = append(rest, a)
		}
	}
	v, _ := s.store.GetSettings(ctx)
	found := 0
	for _, r := range oob.Scan(ctx, rest, v.AMT, v.BMC, 2*time.Second) {
		label := oob.Label(r.Type)
		if r.MAC == "" {
			switch {
			case r.Err != nil:
				sink.Emit(cluster.Warn, "amt", r.IP, "%s answers but the default credentials were refused: %v", label, r.Err)
			case r.Type == "redfish":
				sink.Emit(cluster.Info, "amt", r.IP, "Redfish BMC answers; set default BMC credentials under Kubit settings, or add it by address on the Inventory page")
			default:
				sink.Emit(cluster.Warn, "amt", r.IP, "answers on %s but its MAC is unknown (not on this segment?); add it via its address on the Inventory page", oob.AMTPort)
			}
			continue
		}
		row := store.NodeRow{IP: r.IP, MAC: r.MAC, Source: r.Type, State: "amt"}
		if r.Type == "redfish" {
			row.IP = ""
		}
		existing, err := s.store.GetMachine(ctx, r.MAC)
		known := err == nil && existing.State != "" && existing.State != "amt"
		if known {
			row.State, row.Source = existing.State, existing.Source
			if existing.IP != "" {
				row.IP = existing.IP
			}
		}
		if r.Info != nil {
			row.Serial, row.UUID = r.Info.Serial, r.Info.UUID
			if r.Info.Model != "" && (err != nil || len(existing.Hardware) <= 2) {
				row.Hardware = oobHardware(*r.Info)
			}
		}
		if err := s.store.UpsertNode(ctx, row); err != nil {
			continue
		}
		if r.Info != nil {
			c := v.AMT
			if r.Type == "redfish" {
				c = v.BMC
			}
			c.Type, c.Host = r.Type, r.IP
			_ = s.store.SetMachineOOB(ctx, r.MAC, &c)
			sink.Emit(cluster.Info, "amt", r.IP, "%s %s, %s, power %s", label, r.Info.Version, strings.TrimSpace(r.Info.Manufacturer+" "+r.Info.Model), r.Info.Power)
		} else if r.Err != nil {
			sink.Emit(cluster.Warn, "amt", r.IP, "%s answers but the default credentials were refused: %v", label, r.Err)
		} else {
			sink.Emit(cluster.Info, "amt", r.IP, "%s answers; set default credentials under Kubit settings to identify it", label)
		}
		found++
	}
	sink.End("amt")
	if found > 0 {
		sink.Emit(cluster.Done, "amt", "", "%d machine(s) reachable out of band", found)
	}
	return found
}
