package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/store"
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
	id, err := s.runOperation("c", "test.panic", nil, func(context.Context, cluster.Sink) (any, error) {
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
	next, err := s.runOperation("c", "test.after", nil, func(context.Context, cluster.Sink) (any, error) { return nil, nil })
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

func TestRetryCreateReportsAFailedStart(t *testing.T) {
	c, _ := store.NewCrypto(bytes.Repeat([]byte{12}, 32))
	dir := t.TempDir()
	st, err := store.Open(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := New("test", cluster.NewManager(st, dir), "", c)
	ctx := t.Context()
	failed := func(yaml string) int64 {
		body, _ := json.Marshal(createRequest{YAML: yaml})
		id, err := st.CreateOperation(ctx, "c", "cluster.create", body)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.FinishOperation(ctx, id, "failed"); err != nil {
			t.Fatal(err)
		}
		return id
	}
	retry := func(id int64) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/operations/"+strconv.FormatInt(id, 10)+"/retry", nil)
		req.SetPathValue("id", strconv.FormatInt(id, 10))
		rec := httptest.NewRecorder()
		s.handleOperationRetry(rec, req)
		return rec
	}
	if rec := retry(failed("kind: [")); rec.Code == http.StatusAccepted {
		t.Errorf("an unreadable declaration was accepted: %s", rec.Body)
	}
	id := failed("apiVersion: kubit.dev/v1\nkind: Cluster\nmetadata: {name: c}\nspec:\n  nodes:\n    - {hostname: a, ip: 10.0.0.1, role: controlplane, installDisk: {path: /dev/sda}}\n")
	db, err := sql.Open("sqlite", filepath.Join(dir, "kubit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER refuse BEFORE INSERT ON operations BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}
	rec := retry(id)
	if rec.Code == http.StatusAccepted || !strings.Contains(rec.Body.String(), "refused") {
		t.Errorf("a failed start must not answer accepted: %d %s", rec.Code, rec.Body)
	}
}
