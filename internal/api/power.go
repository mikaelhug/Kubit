package api

import (
	"context"
	"net/http"

	"github.com/mikaelhug/kubit/internal/cluster"
)

const (
	runRestart  = "restart"
	runShutdown = "shutdown"
	runPowerOn  = "power-on"
	runSnapshot = "snapshot"
)

func (s *Server) handleNodePower(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action    string   `json:"action"`
		Hostnames []string `json:"hostnames"`
		Whole     bool     `json:"whole"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len(req.Hostnames) == 0 {
		writeErr(w, badRequest("hostnames must not be empty"))
		return
	}
	name := r.PathValue("name")
	d, err := s.desiredOf(name)
	if err != nil {
		writeErr(w, err)
		return
	}
	st := s.watcher.Latest(name)
	nodes := map[string]cluster.NodeStatus{}
	down := s.manager.OffNodes(name)
	if st != nil {
		for _, n := range st.Nodes {
			nodes[n.Hostname] = n
			if n.Off || !n.TalosReachable {
				down[n.Hostname] = true
			}
		}
	}
	if req.Action == runShutdown {
		whole, err := cluster.CheckShutdown(d.Cluster, req.Hostnames, down)
		if err != nil {
			writeErr(w, conflict(err.Error()))
			return
		}
		if whole != req.Whole {
			writeErr(w, conflict("The cluster changed since the selection; select again."))
			return
		}
	}
	if st == nil {
		writeErr(w, conflict("No status yet; try again in a moment."))
		return
	}
	for _, h := range req.Hostnames {
		if _, ok := nodes[h]; !ok {
			writeErr(w, conflict(h+" is not a node of "+name+"."))
			return
		}
	}
	holder := cluster.Holder("console")
	var run func(context.Context, cluster.Sink) error
	switch req.Action {
	case runRestart:
		if !st.APIReachable {
			writeErr(w, conflict("The Kubernetes API does not answer."))
			return
		}
		if err := allUp(req.Hostnames, nodes); err != nil {
			writeErr(w, err)
			return
		}
		run = func(ctx context.Context, sink cluster.Sink) error {
			return s.manager.RestartNodes(ctx, d, req.Hostnames, holder, sink)
		}
	case runShutdown:
		if err := allUp(req.Hostnames, nodes); err != nil {
			writeErr(w, err)
			return
		}
		run = func(ctx context.Context, sink cluster.Sink) error {
			return s.manager.ShutdownNodes(ctx, d, req.Hostnames, down, req.Whole, holder, sink)
		}
	case runPowerOn:
		for _, h := range req.Hostnames {
			if !down[h] {
				writeErr(w, conflict(h+" is on."))
				return
			}
		}
		run = func(ctx context.Context, sink cluster.Sink) error {
			return s.manager.PowerOnNodes(ctx, d, req.Hostnames, sink)
		}
	default:
		writeErr(w, badRequest("action must be restart, shutdown or power-on"))
		return
	}
	if err := s.startRun(name, req.Action, func(ctx context.Context, sink cluster.Sink) error {
		defer cluster.KeepAwake()()
		return run(ctx, func(e cluster.Event) {
			sink(e)
			if e.Kind == cluster.KindStep {
				s.watcher.CheckNow(name)
			}
		})
	}); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func allUp(hosts []string, nodes map[string]cluster.NodeStatus) error {
	for _, h := range hosts {
		switch n := nodes[h]; {
		case n.Off:
			return conflict(h + " is off.")
		case !n.TalosReachable:
			return conflict(h + " does not answer.")
		}
	}
	return nil
}
