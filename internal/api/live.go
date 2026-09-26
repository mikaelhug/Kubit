package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/mikael/kubit/internal/k8s"
	"github.com/mikael/kubit/internal/store"
)

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
			s.hub.publish(Message{Kind: "cluster", Cluster: row.Name, ClusterRow: row})
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
