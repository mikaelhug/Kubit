package api

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/store"
)

// Message is what SSE subscribers receive: an operation event or a status change.
type Message struct {
	Kind        string              `json:"kind"` // event | operation
	OperationID int64               `json:"operationId"`
	Event       *cluster.Event      `json:"event,omitempty"`
	Operation   *store.OperationRow `json:"operation,omitempty"`
}

// hub fans messages out to SSE subscribers and records them per operation.
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

// runOperation executes fn in the background, persisting every event to the operations
// table and streaming it to subscribers. Operations are serialised per cluster.
func (s *Server) runOperation(cluster, kind string, fn func(ctx context.Context, sink clusterSink) error) (int64, error) {
	ctx := context.Background()
	id, err := s.store.CreateOperation(ctx, cluster, kind)
	if err != nil {
		return 0, err
	}
	s.publishOperation(ctx, id)
	sink := func(e clusterEvent) {
		_ = s.store.AppendOperationLog(ctx, id, e.String())
		s.hub.publish(Message{Kind: "event", OperationID: id, Event: &e})
	}
	go func() {
		s.locks.lock(cluster)
		defer s.locks.unlock(cluster)
		status := "done"
		if err := fn(ctx, sink); err != nil {
			status = "failed"
			_ = s.store.AppendOperationLog(ctx, id, "error: "+err.Error())
			s.hub.publish(Message{Kind: "event", OperationID: id, Event: &clusterEvent{Time: time.Now(), Level: "error", Step: kind, Message: err.Error()}})
		}
		_ = s.store.FinishOperation(ctx, id, status)
		s.publishOperation(ctx, id)
	}()
	return id, nil
}

func (s *Server) publishOperation(ctx context.Context, id int64) {
	if op, err := s.store.GetOperation(ctx, id); err == nil {
		op.Log = ""
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

func marshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
