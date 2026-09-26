package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/config"
)

var lockWait = 5 * time.Second

func (s *Server) holdLock(w http.ResponseWriter, r *http.Request, busy string, keys ...string) (unlock func(), ok bool) {
	ctx, cancel := context.WithTimeout(r.Context(), lockWait)
	defer cancel()
	unlock, err := s.locks.lockAllContext(ctx, keys)
	if err != nil {
		writeErr(w, &statusError{http.StatusConflict, busy})
		return nil, false
	}
	return unlock, true
}

func specLock(name string) string { return "spec:" + name }

const clusterBusy = "An operation is running on this cluster; try again when it finishes."

func (s *Server) editCluster(w http.ResponseWriter, r *http.Request, action, detail string, mutate func(*config.Cluster) error) (*config.Cluster, bool) {
	name := r.PathValue("name")
	unlock, ok := s.holdLock(w, r, clusterBusy, specLock(name))
	if !ok {
		return nil, false
	}
	defer unlock()
	c, _, err := s.manager.LoadCluster(r.Context(), name)
	if err != nil {
		writeErr(w, err)
		return nil, false
	}
	if err := mutate(c); err != nil {
		writeErr(w, err)
		return nil, false
	}
	if err := c.Validate(); err != nil {
		writeErr(w, unprocessable(err))
		return nil, false
	}
	if err := s.manager.SaveCluster(r.Context(), c, ""); err != nil {
		writeErr(w, err)
		return nil, false
	}
	_ = s.store.Audit(r.Context(), name, action, detail)
	return c, true
}

func pathMAC(r *http.Request) string { return strings.ToLower(r.PathValue("mac")) }

func queryMAC(r *http.Request) string { return strings.ToLower(r.URL.Query().Get("mac")) }

func adoptDeclaration(stored, updated *config.Cluster) error {
	if updated.Metadata.Name != stored.Metadata.Name {
		return &statusError{http.StatusUnprocessableEntity, fmt.Sprintf("declaration names cluster %q", updated.Metadata.Name)}
	}
	updated.Spec.SchematicID = stored.Spec.SchematicID
	return nil
}
