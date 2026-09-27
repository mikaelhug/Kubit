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
	"github.com/mikael/kubit/internal/fsx"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
)

func firstControlPlane(ctx context.Context, cps []config.Node, talosconfig []byte, try func(config.Node, *talos.Client) error) (config.Node, *talos.Client, error) {
	var last error
	for _, n := range cps {
		tc, err := talos.Dial(ctx, n.IP, talosconfig)
		if err != nil {
			last = err
			continue
		}
		if err := try(n, tc); err != nil {
			tc.Close()
			last = err
			continue
		}
		return n, tc, nil
	}
	return config.Node{}, nil, last
}

func (m *Manager) snapshotDir(name string) string {
	return filepath.Join(m.ClusterDir(name), "snapshots")
}

func (m *Manager) SnapshotEtcd(ctx context.Context, name, source string, sink Sink) (*store.Snapshot, error) {
	sink.Plan(Steps("pick", "Find a healthy control plane", "snapshot", "Stream the etcd snapshot", "verify", "Verify and seal", "offsite", "Copy off-site", "prune", "Apply retention")...)
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
	err = sink.Run("pick", func() error {
		var err error
		cp, tc, err = firstControlPlane(ctx, c.ControlPlanes(), sec.Talosconfig, func(n config.Node, t *talos.Client) error {
			probe, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			ok, err := t.ServiceHealthy(probe, "etcd")
			if err == nil && !ok {
				err = fmt.Errorf("%s: etcd not healthy", n.Hostname)
			}
			return err
		})
		if err != nil {
			return fmt.Errorf("no control plane with healthy etcd: %w", err)
		}
		sink.Emit(Info, "pick", cp.Hostname, "etcd healthy; taking the snapshot here")
		return nil
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
	if err := sink.Run("snapshot", func() error { return streamSnapshot(ctx, tc, plainPath, &sn, sink) }); err != nil {
		return nil, err
	}
	if err := sink.Run("verify", func() error { return m.sealSnapshot(plainPath, &sn, sink) }); err != nil {
		return nil, err
	}
	sn.ID, err = m.Store.AddSnapshot(ctx, sn)
	if err != nil {
		return nil, err
	}
	sn.TS = ts.Format(time.RFC3339)
	if st, target, oerr := m.Offsite(ctx); errors.Is(oerr, ErrOffsiteOff) {
		sink.Skip("offsite")
	} else {
		_ = sink.Run("offsite", func() error {
			if oerr != nil {
				sink.Emit(Warn, "offsite", "", "off-site target unusable: %v", oerr)
				return oerr
			}
			if cerr := m.CopySnapshotOffsite(ctx, st, &sn); cerr != nil {
				sink.Emit(Warn, "offsite", "", "copy to %s failed: %v", target, cerr)
				return cerr
			}
			sink.Emit(Info, "offsite", "", "copied to %s as %s", target, sn.Offsite)
			return nil
		})
	}
	err = sink.Run("prune", func() error {
		removed, err := m.pruneSnapshots(ctx, name, c.Spec.Backup.Etcd.Keep)
		if err != nil {
			return err
		}
		if removed > 0 {
			sink.Emit(Info, "prune", "", "removed %d scheduled snapshot(s) beyond keep=%d", removed, c.Spec.Backup.Etcd.Keep)
		} else {
			sink.Skip("prune")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	_ = m.Store.Audit(ctx, name, "etcd.snapshot", fmt.Sprintf("%d from %s (%s)", sn.ID, cp.Hostname, source))
	sink.Emit(Done, "prune", "", "snapshot #%d stored", sn.ID)
	return &sn, nil
}

func streamSnapshot(ctx context.Context, tc *talos.Client, plainPath string, sn *store.Snapshot, sink Sink) error {
	call, cancel := context.WithTimeout(ctx, snapshotTimeout)
	defer cancel()
	stream, err := tc.EtcdSnapshot(call)
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
	sink.Emit(Info, "snapshot", sn.Node, "%s received, sha256 %s…", HumanBytes(uint64(n)), sn.SHA256[:12])
	return nil
}

func (m *Manager) sealSnapshot(plainPath string, sn *store.Snapshot, sink Sink) error {
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
	if err := fsx.WriteFile(sn.Path, sealed, 0o600); err != nil {
		return err
	}
	sink.Emit(Info, "verify", "", "%d keys; %s on disk, sealed to %s", keys, HumanBytes(uint64(len(sealed))), filepath.Base(sn.Path))
	return nil
}

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

func (m *Manager) SnapshotAge(ctx context.Context, name string) (age time.Duration, ok bool) {
	last, err := m.Store.LatestSnapshotTS(ctx, name)
	if err != nil || last == "" {
		return 0, false
	}
	t, err := time.Parse(time.RFC3339Nano, last)
	if err != nil {
		return 0, false
	}
	return time.Since(t), true
}

func (m *Manager) SnapshotDue(ctx context.Context, c *config.Cluster) bool {
	iv := c.Spec.Backup.Etcd.IntervalDuration()
	if iv == 0 {
		return false
	}
	age, ok := m.SnapshotAge(ctx, c.Metadata.Name)
	return !ok || age >= iv
}

func (m *Manager) SnapshotStale(ctx context.Context, c *config.Cluster) bool {
	iv := c.Spec.Backup.Etcd.IntervalDuration()
	if iv == 0 {
		return false
	}
	age, ok := m.SnapshotAge(ctx, c.Metadata.Name)
	return ok && age >= 2*iv
}

func (m *Manager) RestoreEtcd(ctx context.Context, name string, snapshotID int64, sink Sink) error {
	sink.Plan(Steps("check", "Verify the snapshot and reach every control plane", "wipe", "Wipe etcd state on every control plane and reboot", "upload", "Upload the snapshot to the first control plane", "bootstrap", "Bootstrap etcd from the snapshot", "ready", "Wait for nodes to become Ready", "workers", "Restart pods on workers")...)
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
	err = sink.Run("check", func() error {
		var err error
		sn, plain, err = m.restoreCheck(ctx, name, snapshotID, cps, sec.Talosconfig, sink)
		return err
	})
	if err != nil {
		return err
	}
	_ = m.Store.SetClusterState(ctx, name, StateProvisioning)
	fail := func(err error) error {
		_ = m.Store.SetClusterState(ctx, name, StateFailed)
		return err
	}
	if err := sink.Run("wipe", func() error { return m.wipeEtcd(ctx, cps, sec.Talosconfig, sink) }); err != nil {
		return fail(err)
	}
	cp1 := cps[0]
	if err := sink.Run("upload", func() error { return m.uploadSnapshot(ctx, cp1, sec.Talosconfig, plain, sink) }); err != nil {
		return fail(err)
	}
	if err := sink.Run("bootstrap", func() error { return m.bootstrapRecovered(ctx, cp1, sec.Talosconfig, len(cps), sink) }); err != nil {
		return fail(err)
	}
	if err := sink.Run("ready", func() error { return m.waitReady(ctx, c, c.Spec.Nodes, sink) }); err != nil {
		return fail(err)
	}
	if len(c.Workers()) == 0 {
		sink.Skip("workers")
	} else if err := sink.Run("workers", func() error { return m.restartWorkerPods(ctx, name, c.Workers(), sink) }); err != nil {
		return fail(err)
	}
	_ = m.Store.Audit(ctx, name, "etcd.restore", fmt.Sprintf("snapshot %d", snapshotID))
	sink.Emit(Done, "workers", "", "cluster %s restored from snapshot #%d (%s)", name, sn.ID, sn.TS)
	return nil
}

func (m *Manager) restoreCheck(ctx context.Context, name string, snapshotID int64, cps []config.Node, talosconfig []byte, sink Sink) (*store.Snapshot, []byte, error) {
	sn, plain, err := m.OpenSnapshot(ctx, snapshotID)
	if err != nil {
		return nil, nil, err
	}
	if sn.Cluster != name {
		return nil, nil, fmt.Errorf("snapshot %d belongs to cluster %s", snapshotID, sn.Cluster)
	}
	sink.Emit(Info, "check", "", "snapshot #%d from %s (%s, %d keys) verified", sn.ID, sn.Node, sn.TS, sn.Keys)
	for _, n := range cps {
		probe, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := talos.Stage(probe, n.IP, talosconfig)
		cancel()
		if err != nil {
			return nil, nil, fmt.Errorf("%s (%s) must answer the Talos API before a restore: %w", n.Hostname, n.IP, err)
		}
	}
	return sn, plain, nil
}

func (m *Manager) wipeEtcd(ctx context.Context, cps []config.Node, talosconfig []byte, sink Sink) error {
	boots := map[string]string{}
	for _, n := range cps {
		tc, err := talos.Dial(ctx, n.IP, talosconfig)
		if err != nil {
			return err
		}
		id, err := readBootID(ctx, tc)
		if err == nil {
			err = resetEphemeral(ctx, tc)
		}
		tc.Close()
		if err != nil {
			return fmt.Errorf("%s: reset: %w", n.Hostname, err)
		}
		boots[n.Hostname] = id
		sink.Emit(Info, "wipe", n.Hostname, "EPHEMERAL wiped, rebooting")
	}
	for _, n := range cps {
		if err := talos.WaitForReboot(ctx, n.IP, talosconfig, boots[n.Hostname], m.Timeouts.Install); err != nil {
			return fmt.Errorf("%s: %w", n.Hostname, err)
		}
		sink.Emit(Info, "wipe", n.Hostname, "back up, waiting for etcd")
	}
	return nil
}

func (m *Manager) uploadSnapshot(ctx context.Context, cp config.Node, talosconfig, plain []byte, sink Sink) error {
	return talos.Retry(ctx, m.Timeouts.Bootstrap, 5*time.Second, func() error {
		call, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		tc, err := talos.Dial(call, cp.IP, talosconfig)
		if err != nil {
			return err
		}
		defer tc.Close()
		if err := tc.EtcdRecoverUpload(call, bytes.NewReader(plain)); err != nil {
			return err
		}
		sink.Emit(Info, "upload", cp.Hostname, "%s uploaded", HumanBytes(uint64(len(plain))))
		return nil
	})
}

func (m *Manager) bootstrapRecovered(ctx context.Context, cp config.Node, talosconfig []byte, members int, sink Sink) error {
	tc, err := talos.Dial(ctx, cp.IP, talosconfig)
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
	sink.Emit(Info, "bootstrap", cp.Hostname, "etcd bootstrapped from the snapshot; waiting for %d members", members)
	return m.waitEtcdMembers(ctx, tc, members)
}

func (m *Manager) restartWorkerPods(ctx context.Context, name string, workers []config.Node, sink Sink) error {
	kc, err := m.KubeClient(ctx, name)
	if err != nil {
		return err
	}
	for _, w := range workers {
		n, err := kc.DeletePodsOnNode(ctx, w.Hostname)
		if err != nil {
			return fmt.Errorf("%s: %w", w.Hostname, err)
		}
		sink.Emit(Info, "workers", w.Hostname, "%d pod(s) deleted; controllers recreate them with fresh watches", n)
	}
	return nil
}
