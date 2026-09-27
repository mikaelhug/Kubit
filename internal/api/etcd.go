package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/store"
)

func (s *Server) etcdRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/clusters/{name}/snapshots", s.handleSnapshots)
	r.HandleFunc("POST /api/v1/clusters/{name}/snapshots", s.handleSnapshotTake)
	r.HandleFunc("GET /api/v1/clusters/{name}/snapshots/{id}", s.handleSnapshotDownload)
	r.HandleFunc("DELETE /api/v1/clusters/{name}/snapshots/{id}", s.handleSnapshotDelete)
	r.HandleFunc("POST /api/v1/clusters/{name}/snapshots/{id}/verify", s.handleSnapshotVerify)
	r.HandleFunc("POST /api/v1/clusters/{name}/snapshots/{id}/restore", s.disruptive(s.handleSnapshotRestore))
}

const scheduleRetry = 10 * time.Minute

func (s *Server) maybeScheduleSnapshot(ctx context.Context, name string, st *cluster.Status) {
	if st.State != cluster.StateReady || !st.APIReachable || !st.Etcd.Healthy || st.Health == cluster.HealthUnknown || st.Observer == cluster.ObserverOffline || s.locks.busy(name) {
		return
	}
	if !s.scheduleDue(name) {
		return
	}
	c, _, err := s.manager.LoadCluster(ctx, name)
	if err != nil || !s.manager.SnapshotDue(ctx, c) || !s.claimSchedule(name) {
		return
	}
	if s.manager.SnapshotStale(ctx, c) && s.observedSince(ctx, c) && !s.store.HasOpenEvent(ctx, name, "", "backup.stale") {
		age, _ := s.manager.SnapshotAge(ctx, name)
		s.raiseEvent(ctx, store.EventRow{Cluster: name, Severity: "warn", Kind: "backup.stale", Message: fmt.Sprintf("Last etcd snapshot is %s old; schedule is every %s. Check the Backups tab for failed snapshot operations.", age.Round(time.Minute), c.Spec.Backup.Etcd.Interval)})
	}
	if _, err := s.runOperation(name, "etcd.snapshot", map[string]string{"source": "schedule"}, func(ctx context.Context, sink cluster.Sink) (any, error) {
		sn, err := s.manager.SnapshotEtcd(ctx, name, "schedule", sink)
		if err == nil {
			_ = s.store.ResolveEvents(ctx, name, "", "backup.stale")
		}
		s.snapshotOffsiteResult(ctx, name, sn, err)
		return sn, err
	}); err != nil {
		log.Printf("snapshot schedule %s: %v", name, err)
	}
}

func (s *Server) scheduleDue(name string) bool {
	s.scheduleMu.Lock()
	defer s.scheduleMu.Unlock()
	return time.Since(s.scheduleAttempt[name]) >= scheduleRetry
}

func (s *Server) claimSchedule(name string) bool {
	s.scheduleMu.Lock()
	defer s.scheduleMu.Unlock()
	if time.Since(s.scheduleAttempt[name]) < scheduleRetry {
		return false
	}
	s.scheduleAttempt[name] = time.Now()
	return true
}

func (s *Server) observedSince(ctx context.Context, c *config.Cluster) bool {
	if s.watcher == nil {
		return true
	}
	age, ok := s.manager.SnapshotAge(ctx, c.Metadata.Name)
	if !ok {
		return true
	}
	due := time.Now().Add(-age).Add(c.Spec.Backup.Etcd.IntervalDuration())
	if s.started.After(due) {
		return false
	}
	if o := s.watcher.Observer(); o.LastGapAt != "" {
		if gap, err := time.Parse(time.RFC3339, o.LastGapAt); err == nil && gap.After(due) {
			return false
		}
	}
	return true
}

func (s *Server) handleSnapshots(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListSnapshots(r.Context(), r.PathValue("name"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleSnapshotTake(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req struct {
		Source string `json:"source"`
	}
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	if req.Source == "" {
		req.Source = "manual"
	}
	s.startOp(w, name, "etcd.snapshot", req, func(ctx context.Context, sink cluster.Sink) (any, error) {
		sn, err := s.manager.SnapshotEtcd(ctx, name, req.Source, sink)
		s.snapshotOffsiteResult(ctx, name, sn, err)
		return sn, err
	})
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

func (s *Server) handleSnapshotDelete(w http.ResponseWriter, r *http.Request) {
	sn, err := s.snapshotOf(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := s.manager.DeleteSnapshot(r.Context(), sn.ID); err != nil {
		writeErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), sn.Cluster, "etcd.snapshot.delete", strconv.FormatInt(sn.ID, 10))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSnapshotVerify(w http.ResponseWriter, r *http.Request) {
	sn, err := s.snapshotOf(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	sn, verr := s.manager.VerifySnapshot(r.Context(), sn.ID)
	out := map[string]any{"snapshot": sn, "ok": verr == nil}
	if verr != nil {
		out["error"] = verr.Error()
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSnapshotRestore(w http.ResponseWriter, r *http.Request) {
	sn, err := s.snapshotOf(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var req struct {
		Confirm string `json:"confirm"`
	}
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	if req.Confirm != sn.Cluster {
		writeErr(w, badRequest(`body must be {"confirm": "<cluster name>"}: restoring wipes etcd on every control plane`))
		return
	}
	s.startOp(w, sn.Cluster, "etcd.restore", map[string]any{"snapshot": sn.ID}, func(ctx context.Context, sink cluster.Sink) (any, error) {
		return nil, s.manager.RestoreEtcd(ctx, sn.Cluster, sn.ID, sink)
	})
}
