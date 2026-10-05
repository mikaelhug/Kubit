package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/mikael/kubit/internal/cluster"
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
		sink.Plan(cluster.Steps("scan", fmt.Sprintf("Probe %d addresses on port 50000", len(addrs)), "record", "Record maintenance nodes")...)
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
		return map[string]int{"found": found}, nil
	})
}
