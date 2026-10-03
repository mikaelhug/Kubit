package api

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/netx"
)

var lockWait = 5 * time.Second

func (s *Server) holdLock(w http.ResponseWriter, r *http.Request, busy string, keys ...string) (unlock func(), ok bool) {
	ctx, cancel := context.WithTimeout(r.Context(), lockWait)
	defer cancel()
	unlock, err := s.locks.lockAllContext(ctx, keys)
	if err != nil {
		writeErr(w, conflict(busy))
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

func pathMAC(r *http.Request) string { return netx.MACKey(r.PathValue("mac")) }

func queryMAC(r *http.Request) string { return netx.MACKey(r.URL.Query().Get("mac")) }

func adoptDeclaration(stored, updated *config.Cluster) error {
	if updated.Metadata.Name != stored.Metadata.Name {
		return &statusError{Status: http.StatusUnprocessableEntity, Msg: fmt.Sprintf("declaration names cluster %q", updated.Metadata.Name)}
	}
	for _, n := range stored.Spec.Nodes {
		if !slices.ContainsFunc(updated.Spec.Nodes, func(u config.Node) bool { return sameNode(n, u) }) {
			return &statusError{Status: http.StatusUnprocessableEntity, Msg: fmt.Sprintf("%s is missing from the declaration; remove nodes with Remove on the Nodes page", n.Hostname)}
		}
	}
	updated.Spec.SchematicID = stored.Spec.SchematicID
	return nil
}

func sameNode(a, b config.Node) bool {
	if a.MAC != "" && b.MAC != "" {
		return netx.MACKey(a.MAC) == netx.MACKey(b.MAC)
	}
	return a.Hostname == b.Hostname
}
