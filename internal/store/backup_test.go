package store_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/mikael/kubit/internal/store"
)

func TestBackupRestoreRoundTrip(t *testing.T) {
	c, _ := store.NewCrypto(bytes.Repeat([]byte{9}, 32))
	home := t.TempDir()
	s, err := store.Open(home, c)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PutCluster(context.Background(), store.ClusterRow{Name: "a", Spec: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	if err := s.Checkpoint(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.PutCluster(context.Background(), store.ClusterRow{Name: "b", Spec: []byte("y")}); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(home, "clusters", "a"), 0o700)
	os.WriteFile(filepath.Join(home, "clusters", "a", "kubeconfig"), []byte("kc"), 0o600)
	os.MkdirAll(filepath.Join(home, "bin"), 0o700)
	os.WriteFile(filepath.Join(home, "bin", "tofu"), []byte("big"), 0o700)
	os.MkdirAll(filepath.Join(home, "vms", "vm-01"), 0o755)
	os.WriteFile(filepath.Join(home, "vms", "vm-01", "disk.raw"), []byte("disk"), 0o644)

	var buf bytes.Buffer
	if err := store.Backup(home, c, &buf); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(buf.Bytes(), []byte("kubeconfig")) {
		t.Error("backup must be sealed: plaintext file names leaked")
	}

	other := t.TempDir()
	written, err := store.Restore(other, c, bytes.NewReader(buf.Bytes()), false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(written, "kubit.db") || !slices.Contains(written, filepath.Join("clusters", "a", "kubeconfig")) {
		t.Errorf("written: %v", written)
	}
	if b, err := os.ReadFile(filepath.Join(other, "clusters", "a", "kubeconfig")); err != nil || string(b) != "kc" {
		t.Errorf("kubeconfig: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(other, "bin", "tofu")); err == nil {
		t.Error("bin/ must not be part of the backup")
	}
	if _, err := os.Stat(filepath.Join(other, "vms")); err == nil {
		t.Error("vms/ must not be part of the backup")
	}
	s2, err := store.Open(other, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if rows, _ := s2.ListClusters(context.Background()); len(rows) != 2 || rows[0].Name != "a" || rows[1].Name != "b" {
		t.Errorf("restored db: %v", rows)
	}
	if _, err := store.Restore(other, c, bytes.NewReader(buf.Bytes()), false); err == nil {
		t.Error("restore into a non-empty home must require --force")
	}
	wrong, _ := store.NewCrypto(bytes.Repeat([]byte{1}, 32))
	if _, err := store.Restore(t.TempDir(), wrong, bytes.NewReader(buf.Bytes()), false); err == nil {
		t.Error("wrong key must fail")
	}
}

func TestRestoreIgnoresHeldLock(t *testing.T) {
	c, _ := store.NewCrypto(bytes.Repeat([]byte{9}, 32))
	src := t.TempDir()
	os.MkdirAll(filepath.Join(src, "clusters", "a"), 0o700)
	os.WriteFile(filepath.Join(src, "clusters", "a", "kubeconfig"), []byte("kc"), 0o600)
	os.WriteFile(filepath.Join(src, "serve.lock"), []byte("stale"), 0o600)
	var buf bytes.Buffer
	if err := store.Backup(src, c, &buf); err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	lock, err := store.LockHome(home)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	before, _ := os.ReadFile(filepath.Join(home, "serve.lock"))
	if _, err := store.Restore(home, c, bytes.NewReader(buf.Bytes()), false); err != nil {
		t.Fatalf("a home holding only the restore lock counts as empty: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(home, "clusters", "a", "kubeconfig")); err != nil || string(b) != "kc" {
		t.Errorf("kubeconfig: %q %v", b, err)
	}
	if after, _ := os.ReadFile(filepath.Join(home, "serve.lock")); string(after) != string(before) {
		t.Errorf("the lock file is not restored over: %q became %q", before, after)
	}
}
