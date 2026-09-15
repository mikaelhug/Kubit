package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/store"
)

// Message is what SSE subscribers receive: an operation event, an operation status
// change, or (later) cluster status and health events.
type Message struct {
	Seq         int64               `json:"seq,omitempty"`
	Kind        string              `json:"kind"` // hello | resync | event | operation | status | health | refresh | cluster | clusterRemoved | machine | machineRemoved | snapshot | snapshotRemoved | audit | settings | healthAck | healthResolved | versions
	OperationID int64               `json:"operationId,omitempty"`
	Event       *cluster.Event      `json:"event,omitempty"`
	Operation   *store.OperationRow `json:"operation,omitempty"`
	Cluster     string              `json:"cluster,omitempty"`
	Status      *cluster.Status     `json:"status,omitempty"`
	Health      *store.EventRow     `json:"health,omitempty"`
	// Scope names the view that changed for kind "refresh" (workloads, network,
	// storage, nodes, machines, snapshots, addons, certificates, settings, pxe).
	Scope string `json:"scope,omitempty"`
	// Typed live-state payloads (one is set per kind).
	ClusterRow *store.ClusterRow `json:"clusterRow,omitempty"`
	Machine    *store.Machine    `json:"machine,omitempty"`
	Snapshot   *store.Snapshot   `json:"snapshot,omitempty"`
	Audit      *store.AuditEntry `json:"audit,omitempty"`
	Settings   *store.Settings   `json:"settings,omitempty"`
	Key        string            `json:"key,omitempty"`  // removed row key, or event id for healthAck ("*" = all)
	Node       string            `json:"node,omitempty"` // healthResolved: object; refresh: unused
	Hello      *Hello            `json:"hello,omitempty"`
}

// Hello opens every live connection: what the client needs to decide between replay
// and resync, and the daemon facts the status bar shows.
type Hello struct {
	Seq       int64  `json:"seq"`
	Version   string `json:"version"`
	StartedAt string `json:"startedAt"`
	Service   bool   `json:"service"`
	PID       int    `json:"pid"`
}

// refresh tells connected consoles that a view of a cluster ("" = Kubit-wide) is
// stale; they refetch exactly that view. This is what replaces polling.
func (s *Server) refresh(cluster string, scopes ...string) {
	for _, sc := range scopes {
		s.hub.publish(Message{Kind: "refresh", Cluster: cluster, Scope: sc})
	}
}

// scopesForKind maps a finished operation to the views it may have changed.
func scopesForKind(kind string) []string {
	// Rows the store owns (clusters, machines, snapshots, settings) are pushed by the
	// change notifier; only derived Kubernetes views need a nudge here.
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

// hub fans messages out to live subscribers and keeps a ring of recent messages so a
// reconnecting console can replay what it missed instead of resyncing.
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

// since returns the messages after seq, or ok=false when seq is older than the ring
// (the client must resync). seq 0 means "just the head".
func (h *hub) since(seq int64) (out []Message, head int64, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	head = h.seq
	if seq >= head {
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
		s.refresh(cluster, scopesForKind(kind)...)
	}()
	return id, nil
}

// Drain cancels every running operation and waits (bounded) for them to record their
// cancelled state, so a daemon stop leaves no operation stuck in "running".
func (s *Server) Drain(timeout time.Duration) {
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
		op.Log = "" // streamed as events; artifacts (plans) are small and wanted live
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
