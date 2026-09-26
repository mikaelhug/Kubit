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
	"strings"
	"sync"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/watch"
)

type Message struct {
	Seq         int64                `json:"seq,omitempty"`
	Kind        string               `json:"kind"`
	OperationID int64                `json:"operationId,omitempty"`
	Event       *cluster.Event       `json:"event,omitempty"`
	Operation   *store.OperationRow  `json:"operation,omitempty"`
	Cluster     string               `json:"cluster,omitempty"`
	Status      *cluster.Status      `json:"status,omitempty"`
	Health      *store.EventRow      `json:"health,omitempty"`
	Scope       string               `json:"scope,omitempty"`
	ClusterRow  *store.ClusterRow    `json:"clusterRow,omitempty"`
	Machine     *nodeView            `json:"machine,omitempty"`
	Snapshot    *store.Snapshot      `json:"snapshot,omitempty"`
	Audit       *store.AuditEntry    `json:"audit,omitempty"`
	Settings    *store.Settings      `json:"settings,omitempty"`
	Sample      *store.Sample        `json:"sample,omitempty"`
	Key         string               `json:"key,omitempty"`
	Node        string               `json:"node,omitempty"`
	Hello       *Hello               `json:"hello,omitempty"`
	Observer    *watch.ObserverState `json:"observer,omitempty"`
}

type Hello struct {
	Seq       int64  `json:"seq"`
	Version   string `json:"version"`
	StartedAt string `json:"startedAt"`
	Service   bool   `json:"service"`
	PID       int    `json:"pid"`
	OS        string `json:"os"`
}

func (s *Server) refresh(cluster string, scopes ...string) {
	for _, sc := range scopes {
		s.hub.publish(Message{Kind: "refresh", Cluster: cluster, Scope: sc})
	}
}

func scopesForKind(kind string) []string {
	switch {
	case strings.HasPrefix(kind, "etcd."), strings.HasPrefix(kind, "node."), strings.HasPrefix(kind, "cluster."), strings.HasPrefix(kind, "upgrade."):
		return []string{"nodes"}
	case strings.HasPrefix(kind, "platform."):
		return []string{"addons", "network"}
	case kind == "cert.rotate":
		return []string{"certificates"}
	}
	return nil
}

type hub struct {
	mu   sync.Mutex
	subs map[chan Message]struct{}
	seq  int64
	ring []Message
	head int
	n    int
}

const ringSize = 2000

func newHub() *hub { return &hub{subs: map[chan Message]struct{}{}, ring: make([]Message, ringSize)} }

func (h *hub) since(seq int64) (out []Message, head int64, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	head = h.seq
	if seq == 0 || seq >= head {
		return nil, head, true
	}
	if seq < head-int64(h.n) {
		return nil, head, false
	}
	for i := 0; i < h.n; i++ {
		m := h.ring[(h.head-h.n+i+ringSize)%ringSize]
		if m.Seq > seq {
			out = append(out, m)
		}
	}
	return out, head, true
}

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
	h.seq++
	m.Seq = h.seq
	h.ring[h.head] = m
	h.head = (h.head + 1) % ringSize
	if h.n < ringSize {
		h.n++
	}
	for ch := range h.subs {
		select {
		case ch <- m:
		default:
		}
	}
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

func (s *Server) runOperationLocking(clusterName string, locks []string, kind string, request any, fn opFunc) (int64, error) {
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
		defer s.cancels.Delete(id)
		defer s.locks.lockAll(locks)()
		status := "done"
		artifact, err := callOperation(ctx, fn, sink)
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

func (l *clusterLocks) lock(name string)   { l.get(name) <- struct{}{} }
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

func (l *clusterLocks) lockAll(names []string) (unlock func()) {
	names = slices.Compact(slices.Sorted(slices.Values(names)))
	for _, n := range names {
		l.lock(n)
	}
	return func() {
		for i := len(names) - 1; i >= 0; i-- {
			l.unlock(names[i])
		}
	}
}
