package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/k8s"
	"github.com/mikael/kubit/internal/offsite"
	"github.com/mikael/kubit/internal/store"
)

func (s *Server) offsiteRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/settings/offsite", s.handleOffsiteStatus)
	r.HandleFunc("POST /api/v1/settings/offsite/test", s.handleOffsiteTest)
	r.HandleFunc("POST /api/v1/settings/offsite/backup", s.handleOffsiteBackup)
}

func (s *Server) handleOffsiteStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.manager.OffsiteStatus(r.Context()))
}

func (s *Server) handleOffsiteTest(w http.ResponseWriter, r *http.Request) {
	var t offsite.Target
	if !decodeJSON(w, r, &t) {
		return
	}
	if t.SecretKey == store.Masked {
		if cur, err := s.store.GetSettings(r.Context()); err == nil {
			t.SecretKey = cur.Offsite.SecretKey
		}
	}
	st, err := offsite.Open(t)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	rtt, err := offsite.Probe(ctx, st)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "roundTripMs": rtt.Milliseconds(), "target": t.String()})
}

func (s *Server) handleOffsiteBackup(w http.ResponseWriter, r *http.Request) {
	id, err := s.startOffsiteBackup()
	accepted(w, id, err)
}

func (s *Server) startOffsiteBackup() (int64, error) {
	return s.runOperation("", "kubit.backup", map[string]string{"source": "offsite"}, func(ctx context.Context, sink cluster.Sink) (any, error) {
		key, err := s.manager.BackupOffsite(ctx, sink)
		s.noteOffsite(ctx, "", err)
		if err != nil {
			s.refresh("", k8s.ScopeOffsite)
			return nil, err
		}
		return map[string]string{"key": key}, nil
	})
}

func (s *Server) noteOffsite(ctx context.Context, name string, err error) {
	if errors.Is(err, cluster.ErrOffsiteOff) {
		return
	}
	if err != nil {
		if !s.store.HasOpenEvent(ctx, name, "", "offsite.failed") {
			what := "etcd snapshot"
			if name == "" {
				what = "Kubit backup"
			}
			s.raiseEvent(ctx, store.EventRow{Cluster: name, Severity: "warn", Kind: "offsite.failed", Message: fmt.Sprintf("Off-site copy of the %s failed: %v. The local copy exists; fix the target under Kubit settings.", what, err)})
		}
		return
	}
	if s.store.HasOpenEvent(ctx, name, "", "offsite.failed") {
		_ = s.store.ResolveEvents(ctx, name, "", "offsite.failed")
		s.raiseEvent(ctx, store.EventRow{Cluster: name, Severity: "info", Kind: "offsite.ok", Message: "Off-site copies are working again."})
	}
}

func (s *Server) snapshotOffsiteResult(ctx context.Context, name string, sn *store.Snapshot, err error) {
	if err != nil || sn == nil {
		return
	}
	if _, target, oerr := s.manager.Offsite(ctx); errors.Is(oerr, cluster.ErrOffsiteOff) {
		return
	} else if oerr != nil {
		s.noteOffsite(ctx, name, oerr)
		return
	} else if sn.Offsite == "" {
		s.noteOffsite(ctx, name, fmt.Errorf("copy to %s did not complete", target))
		return
	}
	s.noteOffsite(ctx, name, nil)
}

func (s *Server) maybeOffsiteBackup(ctx context.Context) {
	s.periodic.every("offsite.backup", 10*time.Minute, func() {
		v, err := s.store.GetSettings(ctx)
		if err != nil || !v.Offsite.Enabled() {
			return
		}
		last, _ := time.Parse(time.RFC3339, s.store.GetValue(ctx, "offsite.lastBackup"))
		if time.Since(last) < 24*time.Hour || s.locks.busy("") {
			return
		}
		if _, err := s.startOffsiteBackup(); err != nil {
			log.Printf("offsite backup: %v", err)
		}
	})
}

func (s *Server) maybeHeartbeat(ctx context.Context) {
	s.periodic.every("heartbeat", 5*time.Minute, func() {
		v, err := s.store.GetSettings(ctx)
		if err != nil || v.Alerts.HeartbeatHours <= 0 || (v.Alerts.WebhookURL == "" && v.Alerts.SMTP.Host == "") {
			return
		}
		last, _ := time.Parse(time.RFC3339, s.store.GetValue(ctx, "alerts.lastHeartbeat"))
		if time.Since(last) < time.Duration(v.Alerts.HeartbeatHours)*time.Hour {
			return
		}
		e := store.EventRow{TS: time.Now().UTC().Format(time.RFC3339), Cluster: "kubit", Severity: "info", Kind: "heartbeat", Message: s.heartbeatText(ctx)}
		s.deliver(v, e)
		_ = s.store.SetValue(ctx, "alerts.lastHeartbeat", e.TS)
	})
}

func (s *Server) heartbeatText(ctx context.Context) string {
	rows, _ := s.store.ListClusters(ctx)
	var b strings.Builder
	fmt.Fprintf(&b, "Kubit %s is watching %d cluster(s).", s.version, len(rows))
	for _, row := range rows {
		alerts := s.store.OpenEventCount(ctx, row.Name)
		snap := "no etcd snapshot"
		if ts, _ := s.store.LatestSnapshotTS(ctx, row.Name); ts != "" {
			if t, err := time.Parse(time.RFC3339, ts); err == nil {
				snap = "last etcd snapshot " + time.Since(t).Round(time.Minute).String() + " ago"
			}
		}
		fmt.Fprintf(&b, "\n- %s: %s, %d open alert(s), %s", row.Name, row.State, alerts, snap)
	}
	if ups := s.updatesAvailable(ctx); len(ups) > 0 {
		b.WriteString("\nUpdates available: " + strings.Join(ups, "; "))
	}
	if machines, err := s.store.ListNodes(ctx, ""); err == nil {
		for _, m := range machines {
			lh := m.LabHost
			if lh == nil {
				continue
			}
			name := lh.Capacity.Hostname
			if name == "" {
				name = m.MAC
			}
			line := fmt.Sprintf("\n- lab host %s: %s, %d VM(s)", name, lh.State, len(lh.VMs))
			if mt := lh.Metrics; mt != nil && mt.DiskTotal > 0 {
				line += fmt.Sprintf(", VM disk %d%% full", mt.DiskUsed*100/mt.DiskTotal)
			}
			if u := lh.Updates; u != nil && (u.Count > 0 || u.NeedsReboot()) {
				line += fmt.Sprintf(", %d package update(s)", u.Count)
				if u.NeedsReboot() {
					line += ", reboot required"
				}
			}
			b.WriteString(line)
		}
	}
	st := s.manager.OffsiteStatus(ctx)
	switch {
	case !st.Enabled:
		b.WriteString("\nOff-site copies: off.")
	case st.Error != "":
		fmt.Fprintf(&b, "\nOff-site (%s): ERROR %s", st.Target, st.Error)
	default:
		fmt.Fprintf(&b, "\nOff-site (%s): %d backup(s), %d snapshot(s), last backup %s", st.Target, st.Backups, st.Snapshots, orNever(st.LastBackup))
	}
	return b.String()
}

func orNever(ts string) string {
	if ts == "" {
		return "never"
	}
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return time.Since(t).Round(time.Minute).String() + " ago"
	}
	return ts
}
