package api

import (
	"strings"
	"testing"
	"time"

	"github.com/mikael/kubit/internal/cluster"
)

func TestStepTrackerDeclaredSteps(t *testing.T) {
	tr := &stepTracker{}
	now := time.Now()
	tr.apply(cluster.Event{Time: now, Kind: cluster.KindSteps, Steps: cluster.Steps("a", "A", "b", "B")})
	tr.apply(cluster.Event{Time: now, Kind: cluster.KindStep, Step: "a", Status: cluster.StepRunning})
	tr.apply(cluster.Event{Time: now, Kind: cluster.KindLog, Step: "a", Message: "x"})
	tr.apply(cluster.Event{Time: now, Kind: cluster.KindStep, Step: "a", Status: cluster.StepDone})
	tr.apply(cluster.Event{Time: now, Kind: cluster.KindStep, Step: "b", Status: cluster.StepRunning})
	tr.finish("failed")
	if len(tr.steps) != 2 {
		t.Fatalf("steps: %+v", tr.steps)
	}
	if tr.steps[0].Status != cluster.StepDone || tr.steps[0].StartedAt == nil || tr.steps[0].FinishedAt == nil {
		t.Errorf("a: %+v", tr.steps[0])
	}
	if tr.steps[1].Status != cluster.StepFailed {
		t.Errorf("running step must fail when the operation fails: %+v", tr.steps[1])
	}
}

func TestStepTrackerDerivesStepsFromLogs(t *testing.T) {
	tr := &stepTracker{}
	now := time.Now()
	tr.apply(cluster.Event{Time: now, Kind: cluster.KindLog, Step: "scan", Message: "x"})
	tr.apply(cluster.Event{Time: now, Kind: cluster.KindLog, Step: "scan", Message: "y"})
	tr.apply(cluster.Event{Time: now, Kind: cluster.KindLog, Step: "record", Message: "z"})
	tr.finish("done")
	if len(tr.steps) != 2 || tr.steps[0].ID != "scan" || tr.steps[0].Status != cluster.StepDone || tr.steps[1].Status != cluster.StepDone {
		t.Errorf("derived steps: %+v", tr.steps)
	}
}

func TestStepTrackerSkipsPendingOnFailure(t *testing.T) {
	tr := &stepTracker{}
	now := time.Now()
	tr.apply(cluster.Event{Time: now, Kind: cluster.KindSteps, Steps: cluster.Steps("a", "A", "b", "B", "c", "C")})
	tr.apply(cluster.Event{Time: now, Kind: cluster.KindStep, Step: "a", Status: cluster.StepRunning})
	tr.apply(cluster.Event{Time: now, Kind: cluster.KindStep, Step: "a", Status: cluster.StepFailed})
	tr.finish("failed")
	if tr.steps[1].Status != cluster.StepSkipped || tr.steps[2].Status != cluster.StepSkipped {
		t.Errorf("pending steps after a failure must read skipped: %+v", tr.steps)
	}
}

func TestOperationPanicFailsTheOperation(t *testing.T) {
	s, st, _ := localServer(t)
	id, err := s.runOperation("c", "test.panic", nil, func(contextT, clusterSink) (any, error) {
		var m map[string]int
		m["boom"]++
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	op := waitOp(t, st, id)
	if op.Status != "failed" || !strings.Contains(op.Log, "internal error") {
		t.Fatalf("status %s, log %q", op.Status, op.Log)
	}
	next, err := s.runOperation("c", "test.after", nil, func(contextT, clusterSink) (any, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	if op := waitOp(t, st, next); op.Status != "done" {
		t.Errorf("the cluster lock must be released after a panic: %s", op.Status)
	}
}

func TestLockAllOrdersAndReleases(t *testing.T) {
	var l clusterLocks
	unlock := l.lockAll([]string{"b", "labhost:x", "a", "b"})
	for _, n := range []string{"a", "b", "labhost:x"} {
		if !l.busy(n) {
			t.Errorf("%s not held", n)
		}
	}
	unlock()
	for _, n := range []string{"a", "b", "labhost:x"} {
		if l.busy(n) {
			t.Errorf("%s still held", n)
		}
	}
}
