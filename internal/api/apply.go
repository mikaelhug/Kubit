package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/mikaelhug/kubit/internal/cluster"
	"github.com/mikaelhug/kubit/internal/repo"
)

type applyLine struct {
	TS      string `json:"ts"`
	Level   string `json:"level"`
	Step    string `json:"step"`
	Node    string `json:"node,omitempty"`
	Message string `json:"message"`
}

type applyRun struct {
	Kind     string      `json:"kind"`
	Running  bool        `json:"running"`
	Started  string      `json:"started"`
	Finished string      `json:"finished,omitempty"`
	Error    string      `json:"error,omitempty"`
	Lines    []applyLine `json:"lines"`
}

func (s *Server) desiredOf(name string) (*cluster.Desired, error) {
	dir, err := s.repoOf(name)
	if err != nil {
		return nil, err
	}
	r, err := repo.Load(dir)
	if err != nil {
		return nil, err
	}
	return cluster.FromRepo(r)
}

func (s *Server) handleApplyRun(w http.ResponseWriter, r *http.Request) {
	s.runsMu.Lock()
	defer s.runsMu.Unlock()
	run := s.runs[r.PathValue("name")]
	if run == nil {
		writeJSON(w, http.StatusOK, applyRun{Lines: []applyLine{}})
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) handleApply(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AllowRemoval bool   `json:"allowRemoval"`
		PlanHash     string `json:"planHash"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	name := r.PathValue("name")
	d, err := s.desiredOf(name)
	if err != nil {
		writeErr(w, err)
		return
	}
	if req.PlanHash == "" {
		writeErr(w, conflict("Review the plan first."))
		return
	}
	if sum, _ := s.latestPlan(name); sum.Hash != req.PlanHash {
		writeErr(w, conflict("The plan changed since you reviewed it; review the new plan."))
		return
	}
	opts := cluster.ConvergeOptions{AllowRemoval: req.AllowRemoval}
	if err := s.startRun(name, "apply", func(ctx context.Context, sink cluster.Sink) error {
		return s.applyReviewed(ctx, name, d, req.PlanHash, opts, sink)
	}); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleDestroy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &req) {
		return
	}
	name := r.PathValue("name")
	d, err := s.desiredOf(name)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := cluster.CheckDestroy(d.Cluster, req.Name); err != nil {
		writeErr(w, conflict(err.Error()))
		return
	}
	if err := s.startRun(name, "destroy", func(ctx context.Context, sink cluster.Sink) error {
		defer cluster.KeepAwake()()
		return s.manager.Destroy(ctx, d, req.Name, cluster.Holder("console"), sink)
	}); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

const scopeApply = "apply"

func (s *Server) startRun(name, kind string, fn func(context.Context, cluster.Sink) error) error {
	s.runsMu.Lock()
	if run := s.runs[name]; run != nil && run.Running {
		s.runsMu.Unlock()
		return conflict("An apply is already running.")
	}
	run := &applyRun{Kind: kind, Running: true, Started: time.Now().UTC().Format(time.RFC3339), Lines: []applyLine{}}
	s.runs[name] = run
	s.runsMu.Unlock()
	s.cancelPlan(name)
	s.setSummary(name, func(sum *planSummary) { sum.State, sum.Holder = planApplying, "" })
	s.refresh(name, scopeApply)
	go s.run(name, run, fn)
	return nil
}

func (s *Server) run(name string, run *applyRun, fn func(context.Context, cluster.Sink) error) {
	sink := func(e cluster.Event) {
		line := applyLine{TS: e.Time.UTC().Format(time.RFC3339), Level: string(e.Level), Step: e.Step, Node: e.Node, Message: e.Message}
		if e.Kind == cluster.KindStep {
			if e.Status != cluster.StepRunning && e.Status != cluster.StepFailed {
				return
			}
			line.Level, line.Message = string(cluster.Info), e.Step
			if e.Status == cluster.StepFailed {
				line.Level, line.Message = string(cluster.Error), e.Step+" failed"
			}
		}
		s.runsMu.Lock()
		run.Lines = append(run.Lines, line)
		s.runsMu.Unlock()
		s.hub.publish(Message{Kind: "apply", Cluster: name, Line: &line})
	}
	err := fn(s.serveCtx, sink)
	s.runsMu.Lock()
	run.Running, run.Finished = false, time.Now().UTC().Format(time.RFC3339)
	if err != nil {
		run.Error = err.Error()
	}
	s.runsMu.Unlock()
	s.refresh(name, scopeApply, scopeConfig, scopeRepo)
	s.kickPlan(name, "applied")
}

func (s *Server) applyReviewed(ctx context.Context, name string, d *cluster.Desired, reviewed string, opts cluster.ConvergeOptions, sink cluster.Sink) error {
	var unlock func()
	if err := sink.Run("lock", func() (err error) {
		unlock, err = s.manager.LockApply(ctx, d, cluster.Holder("console"))
		return err
	}); err != nil {
		return err
	}
	defer unlock()
	var p *cluster.Plan
	if err := sink.Run("review", func() (err error) {
		if p, err = s.manager.Plan(ctx, d, opts); err != nil {
			return err
		}
		if p.Hash != reviewed {
			s.storePlan(name, p)
			return errors.New("the plan changed since you reviewed it; review the new plan")
		}
		if len(p.Problems) > 0 {
			return errors.New("the plan has problems: " + strings.Join(p.Problems, "; "))
		}
		return nil
	}); err != nil {
		return err
	}
	awake := cluster.KeepAwake()
	defer awake()
	return s.converge(ctx, d, p, opts, sink)
}

func (s *Server) converge(ctx context.Context, d *cluster.Desired, p *cluster.Plan, opts cluster.ConvergeOptions, sink cluster.Sink) error {
	if p.Empty() {
		sink.Emit(cluster.Done, "plan", "", "no changes")
		return nil
	}
	return s.manager.Converge(ctx, d, p, opts, sink)
}
