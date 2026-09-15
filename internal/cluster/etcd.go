package cluster

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
)

func (m *Manager) snapshotDir(name string) string {
	return filepath.Join(m.Home, "clusters", name, "snapshots")
}

// SnapshotEtcd takes an etcd snapshot from the first control plane whose etcd is
// healthy, verifies it, seals it with the master key and records it. source is
// manual | schedule | pre-upgrade; scheduled snapshots are pruned to backup.etcd.keep.
func (m *Manager) SnapshotEtcd(ctx context.Context, name, source string, sink Sink) (*store.Snapshot, error) {
	sink.plan(Steps("pick", "Find a healthy control plane", "snapshot", "Stream the etcd snapshot", "verify", "Verify and seal", "offsite", "Copy off-site", "prune", "Apply retention")...)
	c, _, err := m.LoadCluster(ctx, name)
	if err != nil {
		return nil, err
	}
	sec, err := m.Store.GetClusterSecrets(ctx, name)
	if err != nil {
		return nil, err
	}
	var cp config.Node
	var tc *talos.Client
	err = sink.run("pick", func() error {
		var last error
		for _, n := range c.ControlPlanes() {
			dial, cancel := context.WithTimeout(ctx, 10*time.Second)
			t, err := talos.Dial(dial, n.IP, sec.Talosconfig)
			cancel()
			if err != nil {
				last = err
				continue
			}
			probe, cancel := context.WithTimeout(ctx, 10*time.Second)
			ok, err := t.ServiceHealthy(probe, "etcd")
			cancel()
			if err == nil && ok {
				cp, tc = n, t
				sink.emit(Info, "pick", n.Hostname, "etcd healthy; taking the snapshot here")
				return nil
			}
			t.Close()
			if err != nil {
				last = err
			} else {
				last = fmt.Errorf("%s: etcd not healthy", n.Hostname)
			}
		}
		return fmt.Errorf("no control plane with healthy etcd: %w", last)
	})
	if err != nil {
		return nil, err
	}
	defer tc.Close()

	dir := m.snapshotDir(name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	ts := time.Now().UTC()
	plainPath := filepath.Join(dir, ts.Format("20060102T150405Z")+".db")
	sn := store.Snapshot{Cluster: name, Node: cp.Hostname, Source: source, Status: "ok", TalosVersion: c.Spec.TalosVersion, K8sVersion: c.Spec.KubernetesVersion}
	err = sink.run("snapshot", func() error {
		stream, err := tc.EtcdSnapshot(ctx)
		if err != nil {
			return err
		}
		defer stream.Close()
		f, err := os.OpenFile(plainPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		h := sha256.New()
		n, err := io.Copy(io.MultiWriter(f, h), stream)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(plainPath)
			return err
		}
		sn.SizeBytes, sn.SHA256 = n, hex.EncodeToString(h.Sum(nil))
		sink.emit(Info, "snapshot", cp.Hostname, "%s received, sha256 %s…", humanBytes(uint64(n)), sn.SHA256[:12])
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = sink.run("verify", func() error {
		defer os.Remove(plainPath)
		keys, err := talos.VerifySnapshot(plainPath)
		if err != nil {
			return err
		}
		sn.Keys = keys
		plain, err := os.ReadFile(plainPath)
		if err != nil {
			return err
		}
		// bbolt files are mostly free pages: gzip typically shrinks them 20–50×.
		var zb bytes.Buffer
		zw := gzip.NewWriter(&zb)
		if _, err := zw.Write(plain); err != nil {
			return err
		}
		if err := zw.Close(); err != nil {
			return err
		}
		sealed, err := m.Store.SealFile(zb.Bytes())
		if err != nil {
			return err
		}
		sn.Path = plainPath + ".gz.sealed"
		if err := os.WriteFile(sn.Path, sealed, 0o600); err != nil {
			return err
		}
		sink.emit(Info, "verify", "", "%d keys; %s on disk, sealed to %s", keys, humanBytes(uint64(len(sealed))), filepath.Base(sn.Path))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sn.ID, err = m.Store.AddSnapshot(ctx, sn)
	if err != nil {
		return nil, err
	}
	sn.TS = ts.Format(time.RFC3339)
	// A failed copy never fails the snapshot: the local file is the primary; the
	// caller raises offsite.failed so the gap is visible and forwarded.
	if st, target, oerr := m.Offsite(ctx); errors.Is(oerr, ErrOffsiteOff) {
		sink.skip("offsite")
	} else {
		_ = sink.run("offsite", func() error {
			if oerr != nil {
				sink.emit(Warn, "offsite", "", "off-site target unusable: %v", oerr)
				return oerr
			}
			if cerr := m.CopySnapshotOffsite(ctx, st, &sn); cerr != nil {
				sink.emit(Warn, "offsite", "", "copy to %s failed: %v", target, cerr)
				return cerr
			}
			sink.emit(Info, "offsite", "", "copied to %s as %s", target, sn.Offsite)
			return nil
		})
	}
	err = sink.run("prune", func() error {
		removed, err := m.pruneSnapshots(ctx, name, c.Spec.Backup.Etcd.Keep)
		if err != nil {
			return err
		}
		if removed > 0 {
			sink.emit(Info, "prune", "", "removed %d scheduled snapshot(s) beyond keep=%d", removed, c.Spec.Backup.Etcd.Keep)
		} else {
			sink.skip("prune")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	_ = m.Store.Audit(ctx, name, "etcd.snapshot", fmt.Sprintf("%d from %s (%s)", sn.ID, cp.Hostname, source))
	sink.emit(Done, "prune", "", "snapshot #%d stored", sn.ID)
	return &sn, nil
}

// pruneSnapshots deletes the oldest scheduled snapshots beyond keep; manual and
// pre-upgrade snapshots are never pruned automatically.
func (m *Manager) pruneSnapshots(ctx context.Context, name string, keep int) (int, error) {
	all, err := m.Store.ListSnapshots(ctx, name)
	if err != nil {
		return 0, err
	}
	var scheduled []store.Snapshot
	for _, s := range all {
		if s.Source == "schedule" {
			scheduled = append(scheduled, s)
		}
	}
	sort.Slice(scheduled, func(i, j int) bool { return scheduled[i].TS > scheduled[j].TS })
	removed := 0
	for i := keep; i < len(scheduled); i++ {
		if err := m.DeleteSnapshot(ctx, scheduled[i].ID); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

func (m *Manager) DeleteSnapshot(ctx context.Context, id int64) error {
	sn, err := m.Store.GetSnapshot(ctx, id)
	if err != nil {
		return err
	}
	if err := os.Remove(sn.Path); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := m.deleteSnapshotOffsite(ctx, sn); err != nil {
		return fmt.Errorf("remote copy %s: %w", sn.Offsite, err)
	}
	return m.Store.DeleteSnapshot(ctx, id)
}

// OpenSnapshot returns the plain snapshot bytes after checking the hash; a mismatch
// marks the row corrupt.
func (m *Manager) OpenSnapshot(ctx context.Context, id int64) (*store.Snapshot, []byte, error) {
	sn, err := m.Store.GetSnapshot(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	sealed, err := os.ReadFile(sn.Path)
	if err != nil {
		if os.IsNotExist(err) {
			_ = m.Store.SetSnapshotStatus(ctx, id, "missing")
		}
		return sn, nil, err
	}
	plain, err := m.Store.OpenFile(sealed)
	if err != nil {
		_ = m.Store.SetSnapshotStatus(ctx, id, "corrupt")
		return sn, nil, fmt.Errorf("unseal snapshot %d: %w", id, err)
	}
	if zr, zerr := gzip.NewReader(bytes.NewReader(plain)); zerr == nil {
		plain, err = io.ReadAll(zr)
		zr.Close()
		if err != nil {
			_ = m.Store.SetSnapshotStatus(ctx, id, "corrupt")
			return sn, nil, fmt.Errorf("decompress snapshot %d: %w", id, err)
		}
	}
	sum := sha256.Sum256(plain)
	if hex.EncodeToString(sum[:]) != sn.SHA256 {
		_ = m.Store.SetSnapshotStatus(ctx, id, "corrupt")
		return sn, nil, fmt.Errorf("snapshot %d: sha256 mismatch", id)
	}
	if sn.Status != "ok" {
		_ = m.Store.SetSnapshotStatus(ctx, id, "ok")
		sn.Status = "ok"
	}
	return sn, plain, nil
}

// VerifySnapshot re-checks a stored snapshot: unseal, hash, open as bbolt.
func (m *Manager) VerifySnapshot(ctx context.Context, id int64) (*store.Snapshot, error) {
	sn, plain, err := m.OpenSnapshot(ctx, id)
	if err != nil {
		return sn, err
	}
	tmp, err := os.CreateTemp("", "kubit-snap-*.db")
	if err != nil {
		return sn, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(plain); err != nil {
		tmp.Close()
		return sn, err
	}
	tmp.Close()
	if _, err := talos.VerifySnapshot(tmp.Name()); err != nil {
		_ = m.Store.SetSnapshotStatus(ctx, id, "corrupt")
		sn.Status = "corrupt"
		return sn, err
	}
	return sn, nil
}

// SnapshotAge returns how long ago the last good snapshot was taken; ok is false when
// there is none.
func (m *Manager) SnapshotAge(ctx context.Context, name string) (age time.Duration, ok bool) {
	last, err := m.Store.LatestSnapshotTS(ctx, name)
	if err != nil || last == "" {
		return 0, false
	}
	t, err := time.Parse(time.RFC3339Nano, last)
	if err != nil {
		if t, err = time.Parse("2006-01-02T15:04:05.000Z", last); err != nil {
			return 0, false
		}
	}
	return time.Since(t), true
}

// SnapshotDue reports whether the schedule calls for a snapshot now.
func (m *Manager) SnapshotDue(ctx context.Context, c *config.Cluster) bool {
	iv := c.Spec.Backup.Etcd.IntervalDuration()
	if iv == 0 {
		return false
	}
	age, ok := m.SnapshotAge(ctx, c.Metadata.Name)
	return !ok || age >= iv
}

// SnapshotStale reports a schedule that has slipped past twice its interval.
func (m *Manager) SnapshotStale(ctx context.Context, c *config.Cluster) bool {
	iv := c.Spec.Backup.Etcd.IntervalDuration()
	if iv == 0 {
		return false
	}
	age, ok := m.SnapshotAge(ctx, c.Metadata.Name)
	return ok && age >= 2*iv
}

// RestoreEtcd rebuilds the cluster's etcd from a stored snapshot: every control
// plane's EPHEMERAL partition is wiped (STATE keeps the machine config), the snapshot
// is uploaded to the first control plane, etcd is bootstrapped from it and the other
// members rejoin. Workers keep running; their kubelets reconnect once the API is back.
func (m *Manager) RestoreEtcd(ctx context.Context, name string, snapshotID int64, sink Sink) error {
	sink.plan(Steps("check", "Verify the snapshot and reach every control plane", "wipe", "Wipe etcd state on every control plane and reboot", "upload", "Upload the snapshot to the first control plane", "bootstrap", "Bootstrap etcd from the snapshot", "ready", "Wait for nodes to become Ready", "workers", "Restart pods on workers")...)
	c, _, err := m.LoadCluster(ctx, name)
	if err != nil {
		return err
	}
	sec, err := m.Store.GetClusterSecrets(ctx, name)
	if err != nil {
		return err
	}
	cps := c.ControlPlanes()
	var plain []byte
	var sn *store.Snapshot
	err = sink.run("check", func() error {
		var err error
		sn, plain, err = m.OpenSnapshot(ctx, snapshotID)
		if err != nil {
			return err
		}
		if sn.Cluster != name {
			return fmt.Errorf("snapshot %d belongs to cluster %s", snapshotID, sn.Cluster)
		}
		sink.emit(Info, "check", "", "snapshot #%d from %s (%s, %d keys) verified", sn.ID, sn.Node, sn.TS, sn.Keys)
		for _, n := range cps {
			probe, cancel := context.WithTimeout(ctx, 10*time.Second)
			_, err := talos.Stage(probe, n.IP, sec.Talosconfig)
			cancel()
			if err != nil {
				return fmt.Errorf("%s (%s) must answer the Talos API before a restore: %w", n.Hostname, n.IP, err)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	_ = m.Store.SetClusterState(ctx, name, StateProvisioning)
	fail := func(err error) error {
		_ = m.Store.SetClusterState(ctx, name, StateFailed)
		return err
	}
	err = sink.run("wipe", func() error {
		boots := map[string]string{}
		for _, n := range cps {
			dial, cancel := context.WithTimeout(ctx, 15*time.Second)
			tc, err := talos.Dial(dial, n.IP, sec.Talosconfig)
			cancel()
			if err != nil {
				return err
			}
			id, err := tc.BootID(ctx)
			if err == nil {
				err = tc.ResetEphemeral(ctx)
			}
			tc.Close()
			if err != nil {
				return fmt.Errorf("%s: reset: %w", n.Hostname, err)
			}
			boots[n.Hostname] = id
			sink.emit(Info, "wipe", n.Hostname, "EPHEMERAL wiped, rebooting")
		}
		for _, n := range cps {
			if err := talos.WaitForReboot(ctx, n.IP, sec.Talosconfig, boots[n.Hostname], m.Timeouts.Install); err != nil {
				return fmt.Errorf("%s: %w", n.Hostname, err)
			}
			sink.emit(Info, "wipe", n.Hostname, "back up, waiting for etcd")
		}
		return nil
	})
	if err != nil {
		return fail(err)
	}
	cp1 := cps[0]
	err = sink.run("upload", func() error {
		return talos.Retry(ctx, m.Timeouts.Bootstrap, 5*time.Second, func() error {
			call, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			tc, err := talos.Dial(call, cp1.IP, sec.Talosconfig)
			if err != nil {
				return err
			}
			defer tc.Close()
			if err := tc.EtcdRecoverUpload(call, bytesReader(plain)); err != nil {
				return err
			}
			sink.emit(Info, "upload", cp1.Hostname, "%s uploaded", humanBytes(uint64(len(plain))))
			return nil
		})
	})
	if err != nil {
		return fail(err)
	}
	err = sink.run("bootstrap", func() error {
		tc, err := talos.Dial(ctx, cp1.IP, sec.Talosconfig)
		if err != nil {
			return err
		}
		defer tc.Close()
		err = talos.Retry(ctx, m.Timeouts.Bootstrap, 5*time.Second, func() error {
			call, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			return tc.BootstrapRecover(call)
		})
		if err != nil {
			return fmt.Errorf("bootstrap --recover: %w", err)
		}
		sink.emit(Info, "bootstrap", cp1.Hostname, "etcd bootstrapped from the snapshot; waiting for %d members", len(cps))
		return talos.Retry(ctx, m.Timeouts.Bootstrap, 5*time.Second, func() error {
			call, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			ok, err := tc.ServiceHealthy(call, "etcd")
			if err != nil {
				return err
			}
			if !ok {
				return talos.NotReady("etcd not healthy yet")
			}
			n, err := tc.EtcdMemberCount(call)
			if err != nil {
				return err
			}
			if n < len(cps) {
				return talos.NotReady(fmt.Sprintf("etcd has %d/%d members", n, len(cps)))
			}
			return nil
		})
	})
	if err != nil {
		return fail(err)
	}
	if err := sink.run("ready", func() error { return m.waitReady(ctx, c, c.Spec.Nodes, sink) }); err != nil {
		return fail(err)
	}
	// A restore resets resourceVersions; watches held by pods on workers (kube-proxy,
	// CNI, MetalLB speakers) never recover on their own and the node loses service
	// routing. Control planes rebooted; workers get their pods recreated.
	if len(c.Workers()) == 0 {
		sink.skip("workers")
	} else if err := sink.run("workers", func() error {
		kc, err := m.KubeClient(ctx, name)
		if err != nil {
			return err
		}
		for _, w := range c.Workers() {
			n, err := kc.DeletePodsOnNode(ctx, w.Hostname)
			if err != nil {
				return fmt.Errorf("%s: %w", w.Hostname, err)
			}
			sink.emit(Info, "workers", w.Hostname, "%d pod(s) deleted; controllers recreate them with fresh watches", n)
		}
		return nil
	}); err != nil {
		return fail(err)
	}
	_ = m.Store.Audit(ctx, name, "etcd.restore", fmt.Sprintf("snapshot %d", snapshotID))
	sink.emit(Done, "workers", "", "cluster %s restored from snapshot #%d (%s)", name, sn.ID, sn.TS)
	return nil
}

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }
