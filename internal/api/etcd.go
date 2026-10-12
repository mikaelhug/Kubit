package api

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"github.com/mikaelhug/kubit/internal/cluster"
)

const scopeSnapshots = "snapshots"

func (s *Server) etcdRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/clusters/{name}/snapshots", s.handleSnapshots)
	r.HandleFunc("GET /api/v1/clusters/{name}/snapshots/{id}", s.handleSnapshotDownload)
	r.HandleFunc("POST /api/v1/clusters/{name}/snapshots", s.handleSnapshotTake)
}

const snapshotTimeout = 2 * time.Minute

func (s *Server) handleSnapshotTake(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, err := s.repoOf(name); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.startRun(name, runSnapshot, func(ctx context.Context, sink cluster.Sink) error {
		ctx, cancel := context.WithTimeout(ctx, snapshotTimeout)
		defer cancel()
		defer s.refresh(name, scopeSnapshots)
		sn, err := s.manager.SnapshotEtcd(ctx, name, "manual", sink)
		if err != nil {
			return err
		}
		sink.Emit(cluster.Done, "snapshot", "", "snapshot %s written", sn.ID)
		return nil
	}); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleSnapshots(w http.ResponseWriter, r *http.Request) {
	list, err := s.manager.ListSnapshots(r.PathValue("name"))
	reply(w, list, err)
}

func (s *Server) handleSnapshotDownload(w http.ResponseWriter, r *http.Request) {
	sn, err := s.manager.Snapshot(r.PathValue("name"), r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s"`, r.PathValue("name"), filepath.Base(sn.Path)))
	http.ServeFile(w, r, sn.Path)
}
