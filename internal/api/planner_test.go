package api

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mikael/kubit/internal/cluster"
)

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestPlannerNeverRunsDuringAnApply(t *testing.T) {
	s, _ := localServer(t)
	var runs atomic.Int32
	s.plans.run = func(context.Context, string) (*cluster.Plan, error) { runs.Add(1); return &cluster.Plan{}, nil }
	s.runs["lab"] = &applyRun{Running: true}
	s.kickPlan("lab", "repo")
	waitFor(t, "the planner to idle", func() bool {
		s.plans.mu.Lock()
		defer s.plans.mu.Unlock()
		return !s.slot("lab").running
	})
	if sum, _ := s.latestPlan("lab"); runs.Load() != 0 || sum.State != planApplying {
		t.Errorf("no plan while applying: runs %d, %+v", runs.Load(), sum)
	}
}

func TestApplyRefusesAPlanThatWasNotReviewed(t *testing.T) {
	s, _ := servedLab(t)
	for _, body := range []string{`{}`, `{"planHash":"0123"}`} {
		rec := call(t, s, http.MethodPost, "/api/v1/clusters/lab/apply", body)
		if rec.Code != http.StatusConflict || !strings.Contains(strings.ToLower(rec.Body.String()), "review") {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
	sum, p := s.latestPlan("lab")
	if p == nil || sum.Hash == "" {
		t.Fatalf("the refused apply publishes the fresh plan: %+v", sum)
	}
	rec := call(t, s, http.MethodPost, "/api/v1/clusters/lab/apply", `{"planHash":"`+sum.Hash+`"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "problems") {
		t.Errorf("a reviewed plan with problems is still refused: %d %s", rec.Code, rec.Body)
	}
	if s.applying("lab") {
		t.Error("a refused apply leaves no run behind")
	}
}
