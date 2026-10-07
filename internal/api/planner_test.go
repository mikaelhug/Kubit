package api

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mikaelhug/kubit/internal/cluster"
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
	var sum planSummary
	for deadline := time.Now().Add(30 * time.Second); sum.Hash == "" && time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		sum, _ = s.latestPlan("lab")
	}
	if sum.Hash == "" {
		t.Fatal("no plan to review")
	}
	if rec := call(t, s, http.MethodPost, "/api/v1/clusters/lab/apply", `{"planHash":"`+sum.Hash+`"}`); rec.Code != http.StatusAccepted {
		t.Fatalf("a reviewed plan starts an apply: %d %s", rec.Code, rec.Body)
	}
	for deadline := time.Now().Add(30 * time.Second); s.applying("lab") && time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
	}
	s.runsMu.Lock()
	err := s.runs["lab"].Error
	s.runsMu.Unlock()
	if !strings.Contains(err, "problems") {
		t.Errorf("a reviewed plan with problems is still refused: %q", err)
	}
}
