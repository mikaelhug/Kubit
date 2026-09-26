package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/config"
)

var lockWait = 5 * time.Second

func (s *Server) holdLock(w http.ResponseWriter, r *http.Request, key, busy string) (unlock func(), ok bool) {
	ctx, cancel := context.WithTimeout(r.Context(), lockWait)
	defer cancel()
	if err := s.locks.lockContext(ctx, key); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": busy})
		return nil, false
	}
	return func() { s.locks.unlock(key) }, true
}

const clusterBusy = "An operation is running on this cluster; try again when it finishes."

func (s *Server) editCluster(w http.ResponseWriter, r *http.Request, action, detail string, mutate func(*config.Cluster) error) (*config.Cluster, bool) {
	name := r.PathValue("name")
	unlock, ok := s.holdLock(w, r, name, clusterBusy)
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

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	return decodeBody(w, r, v, false)
}

func decodeOptionalJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	return decodeBody(w, r, v, true)
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any, optional bool) bool {
	err := json.NewDecoder(r.Body).Decode(v)
	if err == nil || (optional && errors.Is(err, io.EOF)) {
		return true
	}
	writeErr(w, &statusError{http.StatusBadRequest, "body: " + err.Error()})
	return false
}

func pathMAC(r *http.Request) string { return strings.ToLower(r.PathValue("mac")) }

func unprocessable(err error) error {
	return &statusError{http.StatusUnprocessableEntity, err.Error()}
}

func adoptDeclaration(stored, updated *config.Cluster) error {
	if updated.Metadata.Name != stored.Metadata.Name {
		return &statusError{http.StatusUnprocessableEntity, fmt.Sprintf("declaration names cluster %q", updated.Metadata.Name)}
	}
	updated.Spec.SchematicID = stored.Spec.SchematicID
	return nil
}
