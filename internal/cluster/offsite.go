package cluster

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/mikael/kubit/internal/offsite"
	"github.com/mikael/kubit/internal/store"
)

// ErrOffsiteOff means no target is configured; callers treat it as "nothing to do".
var ErrOffsiteOff = errors.New("off-site copies are off")

// Offsite opens the configured target, or ErrOffsiteOff.
func (m *Manager) Offsite(ctx context.Context) (offsite.Store, offsite.Target, error) {
	v, err := m.Store.GetSettings(ctx)
	if err != nil {
		return nil, offsite.Target{}, err
	}
	if !v.Offsite.Enabled() {
		return nil, v.Offsite, ErrOffsiteOff
	}
	st, err := offsite.Open(v.Offsite)
	return st, v.Offsite, err
}

func snapshotKey(sn *store.Snapshot) string {
	return path.Join("clusters", sn.Cluster, "snapshots", filepath.Base(sn.Path))
}

// CopySnapshotOffsite uploads the sealed snapshot file and records the remote key.
func (m *Manager) CopySnapshotOffsite(ctx context.Context, st offsite.Store, sn *store.Snapshot) error {
	f, err := os.Open(sn.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	key := snapshotKey(sn)
	if err := st.Put(ctx, key, f, info.Size()); err != nil {
		return err
	}
	sn.Offsite = key
	return m.Store.SetSnapshotOffsite(ctx, sn.ID, key)
}

// deleteSnapshotOffsite removes the remote copy when the local one is pruned; a
// missing target is not an error (the copy simply outlives the local file).
func (m *Manager) deleteSnapshotOffsite(ctx context.Context, sn *store.Snapshot) error {
	if sn.Offsite == "" {
		return nil
	}
	st, _, err := m.Offsite(ctx)
	if errors.Is(err, ErrOffsiteOff) {
		return nil
	}
	if err != nil {
		return err
	}
	return st.Delete(ctx, sn.Offsite)
}

// BackupOffsite writes a sealed Kubit backup (the whole of $KUBIT_HOME minus
// caches) to the target under backups/, then prunes to KeepBackups. The archive is
// built in a temp file first so the upload knows its size and a failure leaves no
// partial object behind.
func (m *Manager) BackupOffsite(ctx context.Context, sink Sink) (string, error) {
	sink.plan(Steps("archive", "Build the sealed backup archive", "upload", "Upload to the off-site target", "prune", "Apply retention")...)
	st, target, err := m.Offsite(ctx)
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(m.Home, "backup-*.part")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	var size int64
	if err := sink.run("archive", func() error {
		if err := m.Store.Checkpoint(ctx); err != nil {
			return err
		}
		if err := store.Backup(m.Home, m.Store.Crypto(), tmp); err != nil {
			return err
		}
		info, err := tmp.Stat()
		if err != nil {
			return err
		}
		size = info.Size()
		sink.emit(Info, "archive", "", "%s sealed archive", humanBytes(uint64(size)))
		return nil
	}); err != nil {
		return "", err
	}
	key := path.Join("backups", time.Now().UTC().Format("20060102T150405Z")+".kubitbak")
	if err := sink.run("upload", func() error {
		if _, err := tmp.Seek(0, 0); err != nil {
			return err
		}
		if err := st.Put(ctx, key, tmp, size); err != nil {
			return err
		}
		sink.emit(Info, "upload", "", "%s → %s", key, target)
		return nil
	}); err != nil {
		return "", err
	}
	if err := sink.run("prune", func() error {
		n, err := offsite.PruneOldest(ctx, st, "backups", target.KeepBackups)
		if err != nil {
			return err
		}
		if n == 0 {
			sink.skip("prune")
		} else {
			sink.emit(Info, "prune", "", "removed %d older backup(s) beyond keep=%d", n, target.KeepBackups)
		}
		return nil
	}); err != nil {
		return "", err
	}
	_ = m.Store.SetValue(ctx, "offsite.lastBackup", time.Now().UTC().Format(time.RFC3339))
	_ = m.Store.Audit(ctx, "", "offsite.backup", key)
	sink.emit(Done, "prune", "", "Kubit backup stored off-site as %s", key)
	return key, nil
}

// OffsiteStatus summarises what the target holds, for the settings page.
type OffsiteStatus struct {
	Target     string `json:"target"`
	Enabled    bool   `json:"enabled"`
	LastBackup string `json:"lastBackup,omitempty"`
	Backups    int    `json:"backups"`
	Snapshots  int    `json:"snapshots"`
	Bytes      int64  `json:"bytes"`
	Error      string `json:"error,omitempty"`
}

func (m *Manager) OffsiteStatus(ctx context.Context) OffsiteStatus {
	out := OffsiteStatus{LastBackup: m.Store.GetValue(ctx, "offsite.lastBackup")}
	st, target, err := m.Offsite(ctx)
	out.Target = target.String()
	if errors.Is(err, ErrOffsiteOff) {
		return out
	}
	out.Enabled = true
	if err != nil {
		out.Error = err.Error()
		return out
	}
	lctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	objs, err := st.List(lctx, "")
	if err != nil {
		out.Error = fmt.Sprintf("list: %v", err)
		return out
	}
	for _, o := range objs {
		out.Bytes += o.Size
		switch {
		case path.Dir(o.Key) == "backups":
			out.Backups++
		case path.Base(path.Dir(o.Key)) == "snapshots":
			out.Snapshots++
		}
	}
	return out
}
