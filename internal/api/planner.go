package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mikaelhug/kubit/internal/cluster"
)

const (
	planChecking = "checking"
	planReady    = "ready"
	planBlocked  = "blocked"
	planApplying = "applying"
	planFailed   = "failed"

	scopePlan = "plan"

	driftEvery   = 30 * time.Minute
	planDeadline = 5 * time.Minute
)

type planSummary struct {
	Cluster   string `json:"cluster"`
	State     string `json:"state"`
	Hash      string `json:"hash,omitempty"`
	Changes   int    `json:"changes"`
	OneTime   int    `json:"oneTime"`
	Problems  int    `json:"problems"`
	PlannedAt string `json:"plannedAt,omitempty"`
	Holder    string `json:"holder,omitempty"`
	Error     string `json:"error,omitempty"`
}

type planSlot struct {
	summary   planSummary
	plan      *cluster.Plan
	dirty     bool
	running   bool
	cancel    context.CancelFunc
	signature string
	state     string
}

type planner struct {
	releases sync.Map
	mu       sync.Mutex
	slots    map[string]*planSlot
	run      func(ctx context.Context, name string) (*cluster.Plan, error)
}

func (s *Server) planRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/plans", s.handlePlans)
	r.HandleFunc("GET /api/v1/clusters/{name}/plan", s.handlePlanGet)
	r.HandleFunc("POST /api/v1/clusters/{name}/plan", s.handlePlanKick)
}

func (s *Server) slot(name string) *planSlot {
	if s.plans.slots == nil {
		s.plans.slots = map[string]*planSlot{}
	}
	sl := s.plans.slots[name]
	if sl == nil {
		sl = &planSlot{summary: planSummary{Cluster: name, State: planChecking}}
		s.plans.slots[name] = sl
	}
	return sl
}

func (s *Server) kickPlan(name, reason string) {
	if name == "" {
		return
	}
	s.plans.mu.Lock()
	defer s.plans.mu.Unlock()
	sl := s.slot(name)
	sl.dirty = true
	if reason == "repo" && sl.running && sl.cancel != nil {
		sl.cancel()
	}
	if !sl.running {
		sl.running = true
		go s.planLoop(name)
	}
}

func (s *Server) planLoop(name string) {
	for {
		s.plans.mu.Lock()
		sl := s.slot(name)
		if !sl.dirty {
			sl.running, sl.cancel = false, nil
			s.plans.mu.Unlock()
			return
		}
		sl.dirty = false
		ctx, cancel := context.WithTimeout(s.serveCtx, planDeadline)
		sl.cancel = cancel
		s.plans.mu.Unlock()
		s.planOnce(ctx, name)
		cancel()
	}
}

func (s *Server) cancelPlan(name string) {
	s.plans.mu.Lock()
	defer s.plans.mu.Unlock()
	if sl := s.slot(name); sl.running && sl.cancel != nil {
		sl.cancel()
	}
}

func (s *Server) applying(name string) bool {
	s.runsMu.Lock()
	defer s.runsMu.Unlock()
	run := s.runs[name]
	return run != nil && run.Running
}

func (s *Server) planOnce(ctx context.Context, name string) {
	if s.applying(name) {
		s.setSummary(name, func(p *planSummary) { p.State, p.Holder = planApplying, "" })
		return
	}
	if holder, held := s.manager.ApplyHolder(ctx, name); held {
		s.setSummary(name, func(p *planSummary) { p.State, p.Holder = planApplying, holder })
		s.afterRelease(name)
		return
	}
	s.setSummary(name, func(p *planSummary) { p.State, p.Holder, p.Error = planChecking, "", "" })
	run := s.plans.run
	if run == nil {
		run = s.planFromRepo
	}
	started := time.Now()
	p, err := run(ctx, name)
	if s.applying(name) {
		return
	}
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		err = fmt.Errorf("the plan did not finish within %s", planDeadline)
	case ctx.Err() != nil:
		return
	}
	if err != nil {
		log.Printf("plan %s: %v (after %s)", name, err, time.Since(started).Round(time.Second))
		s.setSummary(name, func(p *planSummary) { p.State, p.Error = planFailed, err.Error() })
		return
	}
	s.storePlan(name, p)
}

func (s *Server) afterRelease(name string) {
	if _, waiting := s.plans.releases.LoadOrStore(name, true); waiting {
		return
	}
	go func() {
		defer s.plans.releases.Delete(name)
		if s.manager.WaitApplyReleased(s.serveCtx, name) == nil {
			s.kickPlan(name, "lease")
		}
	}()
}

func (s *Server) planFromRepo(ctx context.Context, name string) (*cluster.Plan, error) {
	d, err := s.desiredOf(name)
	if err != nil {
		return nil, err
	}
	return s.manager.Plan(ctx, d, cluster.ConvergeOptions{ReadOnly: true})
}

func summaryOf(name string, p *cluster.Plan) planSummary {
	sum := planSummary{Cluster: name, State: planReady, Hash: p.Hash, Problems: len(p.Problems), PlannedAt: time.Now().UTC().Format(time.RFC3339)}
	for _, c := range p.Changes {
		if c.OneTime() {
			sum.OneTime++
		} else {
			sum.Changes++
		}
	}
	if sum.Problems > 0 {
		sum.State = planBlocked
	}
	return sum
}

func (s *Server) storePlan(name string, p *cluster.Plan) {
	s.plans.mu.Lock()
	sl := s.slot(name)
	sl.plan, sl.summary = p, summaryOf(name, p)
	sum := sl.summary
	s.plans.mu.Unlock()
	s.publishPlan(sum)
}

func (s *Server) setSummary(name string, edit func(*planSummary)) {
	s.plans.mu.Lock()
	sl := s.slot(name)
	edit(&sl.summary)
	sum := sl.summary
	s.plans.mu.Unlock()
	s.publishPlan(sum)
}

func (s *Server) publishPlan(sum planSummary) {
	s.hub.publish(Message{Kind: "plan", Cluster: sum.Cluster, Plan: &sum})
	s.refresh(sum.Cluster, scopePlan)
}

func (s *Server) latestPlan(name string) (planSummary, *cluster.Plan) {
	s.plans.mu.Lock()
	defer s.plans.mu.Unlock()
	sl := s.slot(name)
	return sl.summary, sl.plan
}

func (s *Server) noteClusterState(name, state string) {
	s.plans.mu.Lock()
	sl := s.slot(name)
	changed := sl.state != "" && sl.state != state
	sl.state = state
	s.plans.mu.Unlock()
	if changed {
		s.kickPlan(name, "state")
	}
}

func (s *Server) noteStatus(name string, st *cluster.Status) {
	var b strings.Builder
	b.WriteString(boolMark(st.APIReachable))
	for _, n := range st.Nodes {
		b.WriteString("|" + n.Hostname + boolMark(n.TalosReachable) + boolMark(n.Ready) + n.TalosVersion + "/" + n.KubeletVersion)
	}
	sig := b.String()
	s.plans.mu.Lock()
	sl := s.slot(name)
	changed := sl.signature != "" && sl.signature != sig
	sl.signature = sig
	s.plans.mu.Unlock()
	if changed {
		s.kickPlan(name, "cluster")
	}
}

func boolMark(b bool) string {
	if b {
		return "+"
	}
	return "-"
}

func (s *Server) planDrift(ctx context.Context) {
	for {
		wait := driftEvery + rand.N(driftEvery/6)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if s.hub.idle() {
			continue
		}
		for _, r := range s.store.ListClusters() {
			if cluster.Live(r.State) && !s.applying(r.Name) {
				s.kickPlan(r.Name, "drift")
			}
		}
	}
}

func (s *Server) handlePlans(w http.ResponseWriter, _ *http.Request) {
	s.plans.mu.Lock()
	out := []planSummary{}
	for _, sl := range s.plans.slots {
		out = append(out, sl.summary)
	}
	s.plans.mu.Unlock()
	slices.SortFunc(out, func(a, b planSummary) int { return strings.Compare(a.Cluster, b.Cluster) })
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePlanGet(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, err := s.repoOf(name); err != nil {
		writeErr(w, err)
		return
	}
	sum, p := s.latestPlan(name)
	writeJSON(w, http.StatusOK, struct {
		Summary planSummary   `json:"summary"`
		Plan    *cluster.Plan `json:"plan"`
	}{sum, p})
}

func (s *Server) handlePlanKick(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, err := s.repoOf(name); err != nil {
		writeErr(w, err)
		return
	}
	s.kickPlan(name, "manual")
	w.WriteHeader(http.StatusAccepted)
}
