package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/mikael/kubit/internal/cluster"
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

var (
	scheduleMu      sync.Mutex
	scheduleAttempt = map[string]time.Time{}
)

// scheduleRetry is how long a failed scheduled snapshot waits before the next try.
const scheduleRetry = 10 * time.Minute

// maybeScheduleSnapshot runs on every watcher tick: when the schedule is due and the
// cluster is healthy, observed and idle, a snapshot operation is started like any
// other. A failed attempt is retried after scheduleRetry, not on the next tick.
func (s *Server) maybeScheduleSnapshot(ctx context.Context, name string, st *cluster.Status) {
	if st.State != cluster.StateReady || !st.APIReachable || !st.Etcd.Healthy || st.Health == cluster.HealthUnknown || st.Observer == cluster.ObserverOffline || s.locks.busy(name) {
		return
	}
	scheduleMu.Lock()
	defer scheduleMu.Unlock()
	if time.Since(scheduleAttempt[name]) < scheduleRetry {
		return
	}
	c, _, err := s.manager.LoadCluster(ctx, name)
	if err != nil || !s.manager.SnapshotDue(ctx, c) {
		return
	}
	// Staleness is only the schedule's fault when Kubit was awake to run it: a laptop
	// that slept past the interval, or a daemon started after the due time, simply
	// takes the snapshot now.
	if s.manager.SnapshotStale(ctx, c) && s.observedSince(name) && !s.store.HasOpenEvent(ctx, name, "", "backup.stale") {
		age, _ := s.manager.SnapshotAge(ctx, name)
		s.raiseEvent(ctx, store.EventRow{Cluster: name, Severity: "warn", Kind: "backup.stale", Message: fmt.Sprintf("Last etcd snapshot is %s old; schedule is every %s. Check the Backups tab for failed snapshot operations.", age.Round(time.Minute), c.Spec.Backup.Etcd.Interval)})
	}
	scheduleAttempt[name] = time.Now()
	if _, err := s.runOperation(name, "etcd.snapshot", map[string]string{"source": "schedule"}, func(ctx contextT, sink clusterSink) (any, error) {
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

// observedSince reports whether Kubit has been awake and running since the last
// snapshot became due, so a missed schedule is a real failure rather than a nap.
func (s *Server) observedSince(name string) bool {
	if s.watcher == nil {
		return true
	}
	c, _, err := s.manager.LoadCluster(context.Background(), name)
	if err != nil {
		return true
	}
	age, ok := s.manager.SnapshotAge(context.Background(), name)
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

// raiseEvent records a health event and pushes it to SSE subscribers.
func (s *Server) raiseEvent(ctx context.Context, e store.EventRow) {
	id, err := s.store.AddEvent(ctx, e)
	if err != nil {
		return
	}
	e.ID, e.TS = id, time.Now().UTC().Format(time.RFC3339)
	s.hub.publish(Message{Kind: "health", Cluster: e.Cluster, Health: &e})
	s.forwardEvent(e)
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
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Source == "" {
		req.Source = "manual"
	}
	id, err := s.runOperation(name, "etcd.snapshot", req, func(ctx contextT, sink clusterSink) (any, error) {
		sn, err := s.manager.SnapshotEtcd(ctx, name, req.Source, sink)
		s.snapshotOffsiteResult(ctx, name, sn, err)
		return sn, err
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"operationId": id})
}

func (s *Server) snapshotOf(r *http.Request) (*store.Snapshot, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("bad snapshot id")
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

// handleSnapshotDownload streams the plain etcd snapshot, usable with `talosctl
// bootstrap --recover-from` or etcdutl outside Kubit.
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
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Confirm != sn.Cluster {
		http.Error(w, `body must be {"confirm": "<cluster name>"}: restoring wipes etcd on every control plane`, http.StatusBadRequest)
		return
	}
	id, err := s.runOperation(sn.Cluster, "etcd.restore", map[string]any{"snapshot": sn.ID}, func(ctx contextT, sink clusterSink) (any, error) {
		return nil, s.manager.RestoreEtcd(ctx, sn.Cluster, sn.ID, sink)
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"operationId": id})
}
