package api

import (
	"context"
	"time"

	"github.com/mikael/kubit/internal/store"
)

func (s *Server) raiseEvent(ctx context.Context, e store.EventRow) {
	id, err := s.store.AddEvent(ctx, e)
	if err != nil {
		return
	}
	e.ID, e.TS = id, time.Now().UTC().Format(time.RFC3339)
	s.hub.publish(Message{Kind: "health", Cluster: e.Cluster, Health: &e})
	s.forwardEvent(e)
}
