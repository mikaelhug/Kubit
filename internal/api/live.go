package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/mikaelhug/kubit/internal/cluster"
	"github.com/mikaelhug/kubit/internal/store"
	"github.com/mikaelhug/kubit/internal/watch"
)

func (s *Server) liveRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/ws", s.handleLive)
	r.HandleFunc("POST /api/v1/daemon/stop", s.handleDaemonStop)
}

type Message struct {
	Seq        int64             `json:"seq,omitempty"`
	Kind       string            `json:"kind"`
	Cluster    string            `json:"cluster,omitempty"`
	Status     *cluster.Status   `json:"status,omitempty"`
	Health     *watch.Event      `json:"health,omitempty"`
	Scope      string            `json:"scope,omitempty"`
	ClusterRow *store.ClusterRow `json:"clusterRow,omitempty"`
	Machine    *nodeView         `json:"machine,omitempty"`
	Hello      *Hello            `json:"hello,omitempty"`
	Line       *applyLine        `json:"line,omitempty"`
	Plan       *planSummary      `json:"plan,omitempty"`
}

type Hello struct {
	Seq       int64  `json:"seq"`
	Version   string `json:"version"`
	StartedAt string `json:"startedAt"`
	OS        string `json:"os"`
}

func (s *Server) refresh(cluster string, scopes ...string) {
	for _, sc := range scopes {
		s.hub.publish(Message{Kind: "refresh", Cluster: cluster, Scope: sc})
	}
}

const (
	scopeCertificates = "certificates"
	scopeMachines     = "machines"
	scopeConfig       = "config"
)

type frame struct {
	seq  int64
	data []byte
}

type hub struct {
	mu        sync.Mutex
	subs      map[chan frame]struct{}
	seq       int64
	ring      []frame
	head      int
	n         int
	listeners int
	closed    bool
}

const (
	ringSize         = 2000
	subscriberBuffer = 256
)

func newHub() *hub { return &hub{subs: map[chan frame]struct{}{}, ring: make([]frame, ringSize)} }

type subscription struct {
	ch     chan frame
	missed []frame
	head   int64
	replay bool
}

func (h *hub) subscribe(since int64) *subscription {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	h.listeners++
	sub := &subscription{ch: make(chan frame, subscriberBuffer), replay: true}
	h.subs[sub.ch] = struct{}{}
	sub.head = h.seq
	if since == 0 || since >= sub.head {
		return sub
	}
	if since < sub.head-int64(h.n) {
		sub.replay = false
		return sub
	}
	for i := 0; i < h.n; i++ {
		if f := h.ring[(h.head-h.n+i+ringSize)%ringSize]; f.seq > since {
			sub.missed = append(sub.missed, f)
		}
	}
	return sub
}

func (h *hub) unsubscribe(sub *subscription) {
	h.mu.Lock()
	delete(h.subs, sub.ch)
	h.listeners--
	h.mu.Unlock()
}

func (h *hub) shutdown(last Message) {
	h.publish(last)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for ch := range h.subs {
		delete(h.subs, ch)
		close(ch)
	}
}

func (h *hub) idle() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.listeners == 0
}

func (h *hub) publish(m Message) {
	m.Seq = 0
	b, err := json.Marshal(m)
	if err != nil {
		log.Printf("live: %s: %v", m.Kind, err)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	f := frame{seq: h.seq, data: withSeq(b, h.seq)}
	h.ring[h.head] = f
	h.head = (h.head + 1) % ringSize
	if h.n < ringSize {
		h.n++
	}
	for ch := range h.subs {
		select {
		case ch <- f:
		default:
			delete(h.subs, ch)
			close(ch)
		}
	}
}

func withSeq(b []byte, seq int64) []byte {
	out := append(make([]byte, 0, len(b)+24), `{"seq":`...)
	out = strconv.AppendInt(out, seq, 10)
	out = append(out, ',')
	return append(out, b[1:]...)
}

func (s *Server) attachLive(ctx context.Context) {
	s.store.OnChange(func(c store.Change) { s.onChange(ctx, c) })
}

func (s *Server) onChange(ctx context.Context, c store.Change) {
	switch c.Table {
	case "clusters":
		if row, err := s.store.GetCluster(c.Key); err == nil {
			s.hub.publish(Message{Kind: "cluster", Cluster: row.Name, ClusterRow: row})
			s.refresh(row.Name, scopeConfig)
			s.noteClusterState(row.Name, row.State)
		}
	case "machines":
		if c.Key == "" {
			s.refresh("", scopeMachines)
			return
		}
		if m, err := s.store.GetMachine(c.Key); err == nil {
			s.machineSeen(ctx, *m)
			v := s.machineView(*m, s.declaredNodes())
			s.hub.publish(Message{Kind: "machine", Cluster: m.Cluster, Machine: &v})
		}
	case "secrets":
		s.refresh(c.Cluster, scopeCertificates)
	}
}

var devOrigins = []string{"localhost:5173", "127.0.0.1:5173"}

func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	sub := s.hub.subscribe(since)
	if sub == nil {
		writeErr(w, &statusError{Status: http.StatusServiceUnavailable, Msg: "Kubit is stopping."})
		return
	}
	defer s.hub.unsubscribe(sub)
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: devOrigins})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	ctx, stop := context.WithCancel(r.Context())
	defer stop()
	conn.SetReadLimit(1 << 16)
	write := func(b []byte) error {
		wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return conn.Write(wctx, websocket.MessageText, b)
	}
	send := func(m Message) error {
		b, _ := json.Marshal(m)
		return write(b)
	}
	hello := s.hello()
	hello.Seq = sub.head
	if err := send(Message{Kind: "hello", Hello: &hello}); err != nil {
		return
	}
	if !sub.replay {
		if err := send(Message{Kind: "resync"}); err != nil {
			return
		}
	}
	for _, f := range sub.missed {
		if err := write(f.data); err != nil {
			return
		}
	}
	go func() {
		defer stop()
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
		}
	}()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			pctx, pc := context.WithTimeout(ctx, 5*time.Second)
			err := conn.Ping(pctx)
			pc()
			if err != nil {
				return
			}
		case f, ok := <-sub.ch:
			if !ok {
				return
			}
			if err := write(f.data); err != nil {
				log.Printf("live: %v", err)
				return
			}
		}
	}
}

func (s *Server) handleDaemonStop(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusAccepted)
	s.stopDaemon()
}

func (s *Server) hello() Hello {
	return Hello{Version: s.version, StartedAt: s.started.UTC().Format(time.RFC3339), OS: runtime.GOOS}
}

func (s *Server) Close() {
	s.hub.shutdown(Message{Kind: "stopped"})
	deadline := time.Now().Add(2 * time.Second)
	for !s.hub.idle() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
}
