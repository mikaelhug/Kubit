package api

import (
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
