package api

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/store"
)

// Message is what SSE subscribers receive: an operation event, an operation status
// change, or (later) cluster status and health events.
type Message struct {
	Kind        string              `json:"kind"` // event | operation | status | health
	OperationID int64               `json:"operationId,omitempty"`
	Event       *cluster.Event      `json:"event,omitempty"`
	Operation   *store.OperationRow `json:"operation,omitempty"`
	Cluster     string              `json:"cluster,omitempty"`
	Status      *cluster.Status     `json:"status,omitempty"`
	Health      *store.EventRow     `json:"health,omitempty"`
}

// hub fans messages out to SSE subscribers.
type hub struct {
	mu   sync.Mutex
	subs map[chan Message]struct{}
}

func newHub() *hub { return &hub{subs: map[chan Message]struct{}{}} }

func (h *hub) subscribe() (chan Message, func()) {
	ch := make(chan Message, 256)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}

func (h *hub) publish(m Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- m:
		default: // a slow client drops messages rather than blocking operations
		}
	}
}

// opFunc is the body of an operation. It may return an artifact (JSON-serialisable)
// that is stored with the operation, e.g. a plan diff.
type opFunc func(ctx context.Context, sink clusterSink) (artifact any, err error)

// stepTracker keeps the declared steps of one operation up to date from its events.
// Steps that were never declared are created on first mention, so an operation that
// only logs still gets a coarse stepper.
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
			// Undeclared step: the previous running auto-step ends here.
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

// finish closes whatever is still open when the operation ends.
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

// runOperation executes fn in the background, persisting steps, log and artifact and
// streaming events to subscribers. Operations are serialised per cluster and can be
// cancelled through their context.
func (s *Server) runOperation(cluster, kind string, request any, fn opFunc) (int64, error) {
	ctx, cancel := context.WithCancel(context.Background())
	reqJSON, _ := json.Marshal(request)
	id, err := s.store.CreateOperation(ctx, cluster, kind, reqJSON)
	if err != nil {
		cancel()
		return 0, err
	}
	s.cancels.Store(id, cancel)
	s.publishOperation(ctx, id)
	tracker := &stepTracker{}
	sink := func(e clusterEvent) {
		if e.Kind == "" {
			e.Kind = "log"
		}
		if e.Kind == "log" {
			_ = s.store.AppendOperationLog(ctx, id, e.String())
		}
		if tracker.apply(e) {
			_ = s.store.SetOperationSteps(ctx, id, tracker.json())
		}
		s.hub.publish(Message{Kind: "event", OperationID: id, Event: &e})
	}
	go func() {
		defer s.cancels.Delete(id)
		s.locks.lock(cluster)
		defer s.locks.unlock(cluster)
		status := "done"
		artifact, err := fn(ctx, sink)
		bg := context.Background()
		switch {
		case err != nil && errors.Is(err, context.Canceled):
			status = "cancelled"
			_ = s.store.AppendOperationLog(bg, id, "cancelled")
		case err != nil:
			status = "failed"
			_ = s.store.AppendOperationLog(bg, id, "error: "+err.Error())
			s.hub.publish(Message{Kind: "event", OperationID: id, Event: &clusterEvent{Time: time.Now(), Kind: "log", Level: "error", Step: kind, Message: err.Error()}})
		}
		if artifact != nil {
			if b, err := json.Marshal(artifact); err == nil {
				_ = s.store.SetOperationArtifact(bg, id, b)
			}
		}
		tracker.finish(status)
		_ = s.store.SetOperationSteps(bg, id, tracker.json())
		_ = s.store.FinishOperation(bg, id, status)
		s.publishOperation(bg, id)
	}()
	return id, nil
}

// cancelOperation stops a running operation; returns false if none is running.
func (s *Server) cancelOperation(id int64) bool {
	v, ok := s.cancels.Load(id)
	if !ok {
		return false
	}
	v.(context.CancelFunc)()
	return true
}

func (s *Server) publishOperation(ctx context.Context, id int64) {
	if op, err := s.store.GetOperation(ctx, id); err == nil {
		op.Log = ""
		op.Artifact = nil
		s.hub.publish(Message{Kind: "operation", OperationID: id, Operation: op})
	}
}

type (
	clusterSink  = cluster.Sink
	clusterEvent = cluster.Event
	contextT     = context.Context
)

func contextBackground() context.Context { return context.Background() }

// clusterLocks serialises mutating operations per cluster ("" = global, e.g. discovery).
type clusterLocks struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func (l *clusterLocks) get(name string) *sync.Mutex {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.locks == nil {
		l.locks = map[string]*sync.Mutex{}
	}
	m, ok := l.locks[name]
	if !ok {
		m = &sync.Mutex{}
		l.locks[name] = m
	}
	return m
}

func (l *clusterLocks) lock(name string)   { l.get(name).Lock() }
func (l *clusterLocks) unlock(name string) { l.get(name).Unlock() }
func (l *clusterLocks) busy(name string) bool {
	m := l.get(name)
	if m.TryLock() {
		m.Unlock()
		return false
	}
	return true
}

func marshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
