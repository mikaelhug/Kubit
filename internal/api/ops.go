package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"runtime/debug"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/config"
)

func (s *Server) opRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/operations", s.handleOperations)
	r.HandleFunc("GET /api/v1/operations/{id}", s.handleOperation)
	r.HandleFunc("DELETE /api/v1/operations/{id}", s.handleOperationCancel)
	r.HandleFunc("POST /api/v1/operations/{id}/retry", s.handleOperationRetry)
}

type opFunc func(ctx context.Context, sink cluster.Sink) (artifact any, err error)

type stepTracker struct {
	mu    sync.Mutex
	steps []cluster.Step
}

func (t *stepTracker) apply(e cluster.Event) (changed bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := e.Time
	switch e.Kind {
	case cluster.KindSteps:
		for _, s := range e.Steps {
			if t.find(s.ID) == nil {
				t.steps = append(t.steps, s)
			}
		}
		return true
	case cluster.KindStep:
		s := t.find(e.Step)
		if s == nil {
			t.steps = append(t.steps, cluster.Step{ID: e.Step, Title: e.Step, Node: e.Node})
			s = &t.steps[len(t.steps)-1]
		}
		s.Status = e.Status
		switch e.Status {
		case cluster.StepRunning:
			s.StartedAt = &now
		case cluster.StepDone, cluster.StepFailed, cluster.StepSkipped:
			if s.StartedAt == nil {
				s.StartedAt = &now
			}
			s.FinishedAt = &now
		}
		return true
	default:
		if e.Step == "" {
			return false
		}
		s := t.find(e.Step)
		if s == nil {
			for i := range t.steps {
				if t.steps[i].Status == cluster.StepRunning && t.steps[i].Title == t.steps[i].ID {
					t.steps[i].Status = cluster.StepDone
					t.steps[i].FinishedAt = &now
				}
			}
			t.steps = append(t.steps, cluster.Step{ID: e.Step, Title: e.Step, Node: e.Node, Status: cluster.StepRunning, StartedAt: &now})
			return true
		}
		if s.Status == cluster.StepPending {
			s.Status = cluster.StepRunning
			s.StartedAt = &now
			return true
		}
		return false
	}
}

func (t *stepTracker) find(id string) *cluster.Step {
	for i := range t.steps {
		if t.steps[i].ID == id {
			return &t.steps[i]
		}
	}
	return nil
}

func (t *stepTracker) finish(status string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	for i := range t.steps {
		s := &t.steps[i]
		switch {
		case s.Status == cluster.StepRunning && status == "done":
			s.Status, s.FinishedAt = cluster.StepDone, &now
		case s.Status == cluster.StepRunning && status == "cancelled":
			s.Status, s.FinishedAt = cluster.StepCancelled, &now
		case s.Status == cluster.StepRunning:
			s.Status, s.FinishedAt = cluster.StepFailed, &now
		case s.Status == cluster.StepPending && status != "done":
			s.Status = cluster.StepSkipped
		}
	}
}

func (t *stepTracker) json() []byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	b, _ := json.Marshal(t.steps)
	return b
}

func (s *Server) startOp(w http.ResponseWriter, cluster, kind string, request any, fn opFunc) {
	id, err := s.runOperation(cluster, kind, request, fn)
	accepted(w, id, err)
}

func accepted(w http.ResponseWriter, id int64, err error) {
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"operationId": id})
}

func (s *Server) runOperation(cluster, kind string, request any, fn opFunc) (int64, error) {
	return s.runOperationLocking(cluster, []string{cluster}, kind, request, fn)
}

var specWritingKinds = map[string]bool{
	"cluster.create":     true,
	"cluster.apply":      true,
	"node.add":           true,
	"node.remove":        true,
	"node.rename":        true,
	"node.pool":          true,
	"node.readdress":     true,
	"upgrade.talos":      true,
	"upgrade.kubernetes": true,
}

func (s *Server) runOperationLocking(clusterName string, locks []string, kind string, request any, fn opFunc) (int64, error) {
	if specWritingKinds[kind] && clusterName != "" {
		locks = append(slices.Clip(locks), specLock(clusterName))
	}
	ctx, cancel := context.WithCancel(context.Background())
	reqJSON, _ := json.Marshal(request)
	id, err := s.store.CreateOperation(ctx, clusterName, kind, reqJSON)
	if err != nil {
		cancel()
		return 0, err
	}
	s.cancels.Store(id, cancel)
	s.publishOperation(ctx, id)
	tracker := &stepTracker{}
	sink := func(e cluster.Event) {
		if e.Kind == "" {
			e.Kind = "log"
		}
		if e.Kind == "log" {
			logOpWrite(id, "log", s.store.AppendOperationLog(ctx, id, e.String()))
		}
		if tracker.apply(e) {
			logOpWrite(id, "steps", s.store.SetOperationSteps(ctx, id, tracker.json()))
		}
		s.hub.publish(Message{Kind: "event", OperationID: id, Event: &e})
	}
	go func() {
		defer cancel()
		defer s.cancels.Delete(id)
		status := "done"
		var artifact any
		unlock, err := s.locks.lockAllContext(ctx, locks)
		if err == nil {
			defer unlock()
			artifact, err = callOperation(ctx, fn, sink)
		}
		bg := context.Background()
		switch {
		case err != nil && errors.Is(err, context.Canceled):
			status = "cancelled"
			logOpWrite(id, "log", s.store.AppendOperationLog(bg, id, "cancelled"))
		case err != nil:
			status = "failed"
			logOpWrite(id, "log", s.store.AppendOperationLog(bg, id, "error: "+err.Error()))
			s.hub.publish(Message{Kind: "event", OperationID: id, Event: &cluster.Event{Time: time.Now(), Kind: "log", Level: "error", Step: kind, Message: err.Error()}})
		}
		if artifact != nil {
			if b, err := json.Marshal(artifact); err == nil {
				logOpWrite(id, "artifact", s.store.SetOperationArtifact(bg, id, b))
			}
		}
		tracker.finish(status)
		logOpWrite(id, "steps", s.store.SetOperationSteps(bg, id, tracker.json()))
		if err := s.store.FinishOperation(bg, id, status); err != nil {
			log.Printf("operation %d: finish: %v; retrying", id, err)
			time.Sleep(time.Second)
			logOpWrite(id, "finish", s.store.FinishOperation(bg, id, status))
		}
		s.publishOperation(bg, id)
		s.refresh(clusterName, scopesForKind(kind)...)
	}()
	return id, nil
}

func callOperation(ctx context.Context, fn opFunc, sink cluster.Sink) (artifact any, err error) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("operation panic: %v\n%s", p, debug.Stack())
			artifact, err = nil, fmt.Errorf("internal error: %v", p)
		}
	}()
	return fn(ctx, sink)
}

func logOpWrite(id int64, what string, err error) {
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("operation %d: %s: %v", id, what, err)
	}
}

func (s *Server) Drain(timeout time.Duration) {
	s.stop()
	s.cancels.Range(func(_, v any) bool {
		v.(context.CancelFunc)()
		return true
	})
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		n := 0
		s.cancels.Range(func(_, _ any) bool { n++; return true })
		if n == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (s *Server) cancelOperation(id int64) bool {
	v, ok := s.cancels.Load(id)
	if !ok {
		return false
	}
	v.(context.CancelFunc)()
	return true
}

func (s *Server) publishOperation(ctx context.Context, id int64) {
	if op, err := s.store.GetOperationWithoutLog(ctx, id); err == nil {
		s.hub.publish(Message{Kind: "operation", OperationID: id, Operation: op})
	}
}

type clusterLocks struct {
	mu    sync.Mutex
	locks map[string]chan struct{}
}

func (l *clusterLocks) get(name string) chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.locks == nil {
		l.locks = map[string]chan struct{}{}
	}
	m, ok := l.locks[name]
	if !ok {
		m = make(chan struct{}, 1)
		l.locks[name] = m
	}
	return m
}

func (l *clusterLocks) unlock(name string) { <-l.get(name) }
func (l *clusterLocks) busy(name string) bool {
	return len(l.get(name)) > 0
}

func (l *clusterLocks) lockContext(ctx context.Context, name string) error {
	select {
	case l.get(name) <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *clusterLocks) lockAllContext(ctx context.Context, names []string) (unlock func(), err error) {
	names = slices.Compact(slices.Sorted(slices.Values(names)))
	release := func(held []string) {
		for i := len(held) - 1; i >= 0; i-- {
			l.unlock(held[i])
		}
	}
	for i, n := range names {
		if err := l.lockContext(ctx, n); err != nil {
			release(names[:i])
			return nil, err
		}
	}
	return func() { release(names) }, nil
}

func (s *Server) handleOperations(w http.ResponseWriter, r *http.Request) {
	ops, err := s.store.ListOperations(r.Context(), 100)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ops)
}

func (s *Server) handleOperation(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	op, err := s.store.GetOperation(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, op)
}

func (s *Server) handleOperationCancel(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if !s.cancelOperation(id) {
		writeErr(w, conflict("operation is not running"))
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleOperationRetry(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	op, err := s.store.GetOperation(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if op.Status == "running" {
		writeErr(w, conflict("operation is still running"))
		return
	}
	var newID int64
	switch op.Kind {
	case "cluster.create":
		var req createRequest
		_ = json.Unmarshal(op.Request, &req)
		var c *config.Cluster
		if c, err = config.Parse([]byte(req.YAML)); err != nil {
			err = invalid(err)
		} else {
			newID, err = s.startCreate(c, req.SkipPlatform, req)
		}
	case "node.add":
		var n config.Node
		_ = json.Unmarshal(op.Request, &n)
		newID, err = s.startNodeAdd(op.Cluster, n)
	case "platform.apply", "platform.plan":
		newID, err = s.runOperation(op.Cluster, op.Kind, nil, func(ctx context.Context, sink cluster.Sink) (any, error) {
			if op.Kind == "platform.plan" {
				return s.manager.PlanPlatform(ctx, op.Cluster, sink)
			}
			return nil, s.manager.ApplyPlatform(ctx, op.Cluster, sink)
		})
	case "discover":
		var req discoverRequest
		_ = json.Unmarshal(op.Request, &req)
		newID, err = s.startDiscover(req.Targets)
	default:
		writeErr(w, badRequest("this kind of operation cannot be retried; start it again from its page"))
		return
	}
	accepted(w, newID, err)
}
