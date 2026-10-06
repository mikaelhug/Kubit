package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/repo"
)

type applyLine struct {
	TS      string `json:"ts"`
	Level   string `json:"level"`
	Step    string `json:"step"`
	Node    string `json:"node,omitempty"`
	Message string `json:"message"`
}

type applyRun struct {
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
	s.runsMu.Lock()
	if run := s.runs[name]; run != nil && run.Running {
		s.runsMu.Unlock()
		writeErr(w, conflict("An apply is already running."))
		return
	}
	run := &applyRun{Running: true, Started: time.Now().UTC().Format(time.RFC3339), Lines: []applyLine{}}
	s.runs[name] = run
	s.runsMu.Unlock()
	fail := func(err error) {
		s.runsMu.Lock()
		delete(s.runs, name)
		s.runsMu.Unlock()
		writeErr(w, err)
	}
	ctx := s.serveCtx
	host, _ := os.Hostname()
	unlock, err := s.manager.LockApply(ctx, d, "console/"+host)
	if err != nil {
		fail(conflict(err.Error()))
		return
	}
	opts := cluster.ConvergeOptions{AllowRemoval: req.AllowRemoval}
	p, err := s.manager.Plan(ctx, d, opts)
	if err != nil {
		unlock()
		fail(err)
		return
	}
	if p.Hash != req.PlanHash {
		unlock()
		s.storePlan(name, p)
		msg := "The plan changed since you reviewed it; review the new plan."
		if req.PlanHash == "" {
			msg = "Review the plan first."
		}
		fail(conflict(msg))
		return
	}
	if len(p.Problems) > 0 {
		unlock()
		fail(conflict("The plan has problems: " + strings.Join(p.Problems, "; ")))
		return
	}
	s.setSummary(name, func(sum *planSummary) { sum.State, sum.Holder = planApplying, "" })
	s.refresh(name, scopeApply)
	go s.apply(ctx, name, d, p, opts, run, unlock)
	w.WriteHeader(http.StatusAccepted)
}

const scopeApply = "apply"

func (s *Server) apply(ctx context.Context, name string, d *cluster.Desired, p *cluster.Plan, opts cluster.ConvergeOptions, run *applyRun, unlock func()) {
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
	awake := cluster.KeepAwake()
	err := s.converge(ctx, d, p, opts, sink)
	awake()
	unlock()
	s.runsMu.Lock()
	run.Running, run.Finished = false, time.Now().UTC().Format(time.RFC3339)
	if err != nil {
		run.Error = err.Error()
	}
	s.runsMu.Unlock()
	s.refresh(name, scopeApply, scopeConfig, scopeRepo)
	s.kickPlan(name, "applied")
}

func (s *Server) converge(ctx context.Context, d *cluster.Desired, p *cluster.Plan, opts cluster.ConvergeOptions, sink cluster.Sink) error {
	if p.Empty() {
		sink.Emit(cluster.Done, "plan", "", "no changes")
		return nil
	}
	return s.manager.Converge(ctx, d, p, opts, sink)
}
