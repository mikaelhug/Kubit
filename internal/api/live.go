package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/k8s"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/watch"
)

func (s *Server) liveRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/version", s.handleVersion)
	r.HandleFunc("GET /api/v1/ws", s.handleLive)
}

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
	ClusterRow  *clusterSummary      `json:"clusterRow,omitempty"`
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

const scopeCertificates = "certificates"

func scopesForKind(kind string) []string {
	switch {
	case kind == "cluster.create":
		return []string{k8s.ScopeNodes, k8s.ScopeAddons, k8s.ScopeNetwork, k8s.ScopeFlux, scopeCertificates, "sops"}
	case strings.HasPrefix(kind, "platform."):
		return []string{k8s.ScopeAddons, k8s.ScopeNetwork, k8s.ScopeFlux}
	case kind == "cert.rotate":
		return []string{scopeCertificates}
	case strings.HasPrefix(kind, "etcd."), strings.HasPrefix(kind, "node."), strings.HasPrefix(kind, "cluster."), strings.HasPrefix(kind, "upgrade."):
		return []string{k8s.ScopeNodes}
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

func (s *Server) attachLive(ctx context.Context) {
	s.store.OnChange(func(c store.Change) { s.onChange(ctx, c) })
	go s.store.WatchExternal(ctx, 2*time.Second)
}

func (s *Server) onChange(ctx context.Context, c store.Change) {
	switch c.Table {
	case "*":
		s.hub.publish(Message{Kind: "resync"})
	case "clusters":
		if c.Op == "delete" {
			s.hub.publish(Message{Kind: "clusterRemoved", Cluster: c.Cluster, Key: c.Key})
			return
		}
		if row, err := s.store.GetCluster(ctx, c.Key); err == nil {
			s.hub.publish(Message{Kind: "cluster", Cluster: row.Name, ClusterRow: summarize(*row)})
		}
	case "machines":
		if c.Op == "delete" {
			s.hub.publish(Message{Kind: "machineRemoved", Key: c.Key})
			return
		}
		if c.Key == "" {
			if rows, err := s.store.ListNodes(ctx, ""); err == nil {
				for i := range rows {
					v := machineView(rows[i])
					s.hub.publish(Message{Kind: "machine", Cluster: rows[i].Cluster, Machine: &v})
				}
			}
			return
		}
		m, err := s.store.GetMachine(ctx, c.Key)
		if err != nil {
			m, err = s.store.GetNode(ctx, c.Key)
		}
		if err == nil {
			v := machineView(*m)
			s.hub.publish(Message{Kind: "machine", Cluster: m.Cluster, Machine: &v})
		}
	case "snapshots":
		id, _ := strconv.ParseInt(c.Key, 10, 64)
		if c.Op == "delete" {
			s.hub.publish(Message{Kind: "snapshotRemoved", Cluster: c.Cluster, Key: c.Key})
			s.refresh("", k8s.ScopeOffsite)
			return
		}
		if sn, err := s.store.GetSnapshot(ctx, id); err == nil {
			s.hub.publish(Message{Kind: "snapshot", Cluster: sn.Cluster, Snapshot: sn})
			if sn.Offsite != "" {
				s.refresh("", k8s.ScopeOffsite)
			}
		}
	case "audit":
		if rows, err := s.store.ListAudit(ctx, "", 1); err == nil && len(rows) == 1 {
			s.hub.publish(Message{Kind: "audit", Cluster: rows[0].Cluster, Audit: &rows[0]})
		}
	case "users":
		s.hub.publish(Message{Kind: "refresh", Scope: "users"})
	case "secrets":
		s.refresh(c.Cluster, scopeCertificates)
	case "sops":
		s.hub.publish(Message{Kind: "refresh", Cluster: c.Cluster, Scope: "sops"})
	case "settings":
		if offsiteKeys[c.Key] {
			s.refresh("", k8s.ScopeOffsite)
		}
		if v, err := s.store.GetSettings(ctx); err == nil {
			v = redactSettings(v)
			s.hub.publish(Message{Kind: "settings", Settings: &v})
		}
	case "events":
		switch c.Op {
		case "ack":
			s.hub.publish(Message{Kind: "healthAck", Cluster: c.Cluster, Key: c.Key})
		case "resolve":
			s.hub.publish(Message{Kind: "healthResolved", Cluster: c.Cluster, Key: c.Key, Node: c.Node})
		}
	case "operations":
		if id, err := strconv.ParseInt(c.Key, 10, 64); err == nil {
			s.publishOperation(ctx, id)
		}
	}
}

var offsiteKeys = map[string]bool{"kubit": true, "offsite.lastBackup": true}

var devOrigins = []string{"localhost:5173", "127.0.0.1:5173"}

func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: devOrigins})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	ctx, stop := context.WithCancel(r.Context())
	defer stop()
	conn.SetReadLimit(1 << 16)
	send := func(m Message) error {
		wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		b, _ := json.Marshal(m)
		return conn.Write(wctx, websocket.MessageText, b)
	}
	ch, cancel := s.hub.subscribe()
	defer cancel()
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	missed, head, ok := s.hub.since(since)
	hello := s.hello()
	hello.Seq = head
	if err := send(Message{Kind: "hello", Hello: &hello}); err != nil {
		return
	}
	if !ok {
		if err := send(Message{Kind: "resync"}); err != nil {
			return
		}
	}
	for _, m := range missed {
		if err := send(m); err != nil {
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
		case m := <-ch:
			if err := send(m); err != nil {
				log.Printf("live: %v", err)
				return
			}
		}
	}
}

func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	h := s.hello()
	writeJSON(w, http.StatusOK, map[string]any{"kubit": h.Version, "startedAt": h.StartedAt, "service": h.Service, "pid": h.PID, "os": h.OS})
}

func (s *Server) hello() Hello {
	return Hello{Version: s.version, StartedAt: s.started.UTC().Format(time.RFC3339), Service: os.Getenv("KUBIT_SERVICE") != "", PID: os.Getpid(), OS: runtime.GOOS}
}
