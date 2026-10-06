package cluster

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/fsx"
	"github.com/mikael/kubit/internal/repo"
	"github.com/mikael/kubit/internal/sops"
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

const snapshotExt = ".db.gz.age"

type Snapshot struct {
	ID        string `json:"id"`
	TS        string `json:"ts"`
	Source    string `json:"source"`
	SizeBytes int64  `json:"sizeBytes"`
	Path      string `json:"-"`
}

func (m *Manager) snapshotDir(name string) (string, error) {
	d, err := m.Desired(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(d.Dir, repo.SnapshotDir), nil
}

func (m *Manager) SnapshotEtcd(ctx context.Context, name, source string, sink Sink) (*Snapshot, error) {
	c, _, err := m.LoadCluster(name)
	if err != nil {
		return nil, err
	}
	d, err := m.Desired(name)
	if err != nil {
		return nil, err
	}
	rule, err := sops.RuleFor(d.Dir, filepath.Join(d.Dir, repo.SecretsFile))
	if err != nil {
		return nil, err
	}
	var cp config.Node
	var tc *talos.Client
	err = sink.Run("pick", func() error {
		var err error
		cp, tc, err = firstControlPlane(ctx, c.ControlPlanes(), d.Talosconfig, func(n config.Node, t *talos.Client) error {
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

	dir := filepath.Join(d.Dir, repo.SnapshotDir)
	if err := repo.Ignore(d.Dir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	ts := time.Now().UTC()
	sn := Snapshot{ID: ts.Format("20060102T150405Z") + "-" + source, TS: ts.Format(time.RFC3339), Source: source}
	tmp, err := os.CreateTemp("", "kubit-etcd-*.db")
	if err != nil {
		return nil, err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	if err := sink.Run("snapshot", func() error { return streamSnapshot(ctx, tc, tmp.Name(), cp.Hostname, sink) }); err != nil {
		return nil, err
	}
	sn.Path = filepath.Join(dir, sn.ID+snapshotExt)
	if err := sink.Run("verify", func() error { return encryptSnapshot(tmp.Name(), sn.Path, rule.Age, sink) }); err != nil {
		return nil, err
	}
	if fi, err := os.Stat(sn.Path); err == nil {
		sn.SizeBytes = fi.Size()
	}
	sink.Emit(Done, "verify", "", "snapshot %s written", sn.Path)
	return &sn, nil
}

func streamSnapshot(ctx context.Context, tc *talos.Client, path, node string, sink Sink) error {
	call, cancel := context.WithTimeout(ctx, snapshotTimeout)
	defer cancel()
	stream, err := tc.EtcdSnapshot(call)
	if err != nil {
		return err
	}
	defer stream.Close()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, stream)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	sink.Emit(Info, "snapshot", node, "%s received", HumanBytes(uint64(n)))
	return nil
}

func encryptSnapshot(plainPath, path string, recipients []string, sink Sink) error {
	keys, err := talos.VerifySnapshot(plainPath)
	if err != nil {
		return err
	}
	plain, err := os.ReadFile(plainPath)
	if err != nil {
		return err
	}
	var rcpts []age.Recipient
	for _, r := range recipients {
		rc, err := age.ParseX25519Recipient(r)
		if err != nil {
			return fmt.Errorf("recipient %s: %w", r, err)
		}
		rcpts = append(rcpts, rc)
	}
	if len(rcpts) == 0 {
		return errors.New("no age recipient in .sops.yaml")
	}
	var out bytes.Buffer
	aw, err := age.Encrypt(&out, rcpts...)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(aw)
	if _, err := zw.Write(plain); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	if err := aw.Close(); err != nil {
		return err
	}
	if err := fsx.WriteFile(path, out.Bytes(), 0o600); err != nil {
		return err
	}
	sink.Emit(Info, "verify", "", "%d keys; %s encrypted for %d recipient(s)", keys, HumanBytes(uint64(out.Len())), len(rcpts))
	return nil
}

func (m *Manager) ListSnapshots(name string) ([]Snapshot, error) {
	dir, err := m.snapshotDir(name)
	if err != nil {
		return nil, err
	}
	return listSnapshots(dir)
}

func SnapshotsIn(repoDir string) ([]Snapshot, error) {
	return listSnapshots(filepath.Join(repoDir, repo.SnapshotDir))
}

func listSnapshots(dir string) ([]Snapshot, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return []Snapshot{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Snapshot{}
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), snapshotExt)
		if !ok || e.IsDir() {
			continue
		}
		stamp, source, _ := strings.Cut(id, "-")
		ts, err := time.Parse("20060102T150405Z", stamp)
		if err != nil {
			continue
		}
		sn := Snapshot{ID: id, TS: ts.Format(time.RFC3339), Source: source, Path: filepath.Join(dir, e.Name())}
		if fi, err := e.Info(); err == nil {
			sn.SizeBytes = fi.Size()
		}
		out = append(out, sn)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

func (m *Manager) Snapshot(name, id string) (*Snapshot, error) {
	list, err := m.ListSnapshots(name)
	if err != nil {
		return nil, err
	}
	for _, sn := range list {
		if sn.ID == id {
			return &sn, nil
		}
	}
	return nil, fmt.Errorf("snapshot %s: %w", id, store.ErrNotFound)
}

func openSnapshot(sn *Snapshot) ([]byte, error) {
	ids, err := sops.Identities()
	if err != nil {
		return nil, err
	}
	enc, err := os.ReadFile(sn.Path)
	if err != nil {
		return nil, err
	}
	ar, err := age.Decrypt(bytes.NewReader(enc), ids...)
	if err != nil {
		return nil, fmt.Errorf("snapshot %s: %w", sn.ID, err)
	}
	zr, err := gzip.NewReader(ar)
	if err != nil {
		return nil, fmt.Errorf("snapshot %s: %w", sn.ID, err)
	}
	defer zr.Close()
	plain, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("snapshot %s: %w", sn.ID, err)
	}
	tmp, err := os.CreateTemp("", "kubit-restore-*.db")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(plain)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, err
	}
	if _, err := talos.VerifySnapshot(tmp.Name()); err != nil {
		return nil, fmt.Errorf("snapshot %s: %w", sn.ID, err)
	}
	return plain, nil
}

func (m *Manager) RestoreEtcd(ctx context.Context, name, snapshotID string, sink Sink) error {
	c, _, err := m.LoadCluster(name)
	if err != nil {
		return err
	}
	sec, err := m.Store.GetClusterSecrets(name)
	if err != nil {
		return err
	}
	cps := c.ControlPlanes()
	var plain []byte
	var sn *Snapshot
	err = sink.Run("check", func() error {
		var err error
		sn, plain, err = m.restoreCheck(ctx, name, snapshotID, cps, sec.Talosconfig, sink)
		return err
	})
	if err != nil {
		return err
	}
	if err := sink.Run("wipe", func() error { return m.wipeEtcd(ctx, cps, sec.Talosconfig, sink) }); err != nil {
		return err
	}
	cp1 := cps[0]
	if err := sink.Run("upload", func() error { return m.uploadSnapshot(ctx, cp1, sec.Talosconfig, plain, sink) }); err != nil {
		return err
	}
	if err := sink.Run("bootstrap", func() error { return m.bootstrapRecovered(ctx, cp1, sec.Talosconfig, len(cps), sink) }); err != nil {
		return err
	}
	if err := sink.Run("ready", func() error { return m.waitReady(ctx, c, c.Spec.Nodes, sink) }); err != nil {
		return err
	}
	if len(c.Workers()) > 0 {
		if err := sink.Run("workers", func() error { return m.restartWorkerPods(ctx, name, c.Workers(), sink) }); err != nil {
			return err
		}
	}
	sink.Emit(Done, "workers", "", "cluster %s restored from snapshot %s", name, sn.ID)
	return nil
}

func (m *Manager) restoreCheck(ctx context.Context, name, snapshotID string, cps []config.Node, talosconfig []byte, sink Sink) (*Snapshot, []byte, error) {
	sn, err := m.Snapshot(name, snapshotID)
	if err != nil {
		return nil, nil, err
	}
	plain, err := openSnapshot(sn)
	if err != nil {
		return nil, nil, err
	}
	sink.Emit(Info, "check", "", "snapshot %s (%s) verified", sn.ID, HumanBytes(uint64(len(plain))))
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
		if err := talos.WaitForReboot(ctx, n.IP, talosconfig, boots[n.Hostname], installTimeout); err != nil {
			return fmt.Errorf("%s: %w", n.Hostname, err)
		}
		sink.Emit(Info, "wipe", n.Hostname, "back up, waiting for etcd")
	}
	return nil
}

func (m *Manager) uploadSnapshot(ctx context.Context, cp config.Node, talosconfig, plain []byte, sink Sink) error {
	return talos.Retry(ctx, bootstrapTimeout, 5*time.Second, func() error {
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
	err = talos.Retry(ctx, bootstrapTimeout, 5*time.Second, func() error {
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
