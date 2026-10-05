package api

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/mikael/kubit/internal/store"
)

func (s *Server) etcdRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/clusters/{name}/snapshots", s.handleSnapshots)
	r.HandleFunc("GET /api/v1/clusters/{name}/snapshots/{id}", s.handleSnapshotDownload)
}

func (s *Server) handleSnapshots(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListSnapshots(r.Context(), r.PathValue("name"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) snapshotOf(r *http.Request) (*store.Snapshot, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return nil, badRequest("bad snapshot id")
	}
	sn, err := s.store.GetSnapshot(r.Context(), id)
	if err != nil {
		return nil, err
	}
	if sn.Cluster != r.PathValue("name") {
		return nil, fmt.Errorf("snapshot %d: %w", id, store.ErrNotFound)
	}
	return sn, nil
}

func (s *Server) handleSnapshotDownload(w http.ResponseWriter, r *http.Request) {
	sn, err := s.snapshotOf(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	_, plain, err := s.manager.OpenSnapshot(r.Context(), sn.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-etcd-%s.db"`, sn.Cluster, sn.TS))
	_, _ = w.Write(plain)
}
