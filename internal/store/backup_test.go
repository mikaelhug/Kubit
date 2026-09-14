package store_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
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
	// Back up while the store is open, as the daemon does.
	defer s.Close()
	os.MkdirAll(filepath.Join(home, "clusters", "a"), 0o700)
	os.WriteFile(filepath.Join(home, "clusters", "a", "kubeconfig"), []byte("kc"), 0o600)
	os.MkdirAll(filepath.Join(home, "bin"), 0o700)
	os.WriteFile(filepath.Join(home, "bin", "tofu"), []byte("big"), 0o700)

	var buf bytes.Buffer
	if err := store.Backup(home, c, &buf); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(buf.Bytes(), []byte("kubeconfig")) {
		t.Error("backup must be sealed: plaintext file names leaked")
	}

	other := t.TempDir()
	if err := store.Restore(other, c, bytes.NewReader(buf.Bytes()), false); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(other, "clusters", "a", "kubeconfig")); err != nil || string(b) != "kc" {
		t.Errorf("kubeconfig: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(other, "bin", "tofu")); err == nil {
		t.Error("bin/ must not be part of the backup")
	}
	s2, err := store.Open(other, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if rows, _ := s2.ListClusters(context.Background()); len(rows) != 1 || rows[0].Name != "a" {
		t.Errorf("restored db: %v", rows)
	}
	if err := store.Restore(other, c, bytes.NewReader(buf.Bytes()), false); err == nil {
		t.Error("restore into a non-empty home must require --force")
	}
	wrong, _ := store.NewCrypto(bytes.Repeat([]byte{1}, 32))
	if err := store.Restore(t.TempDir(), wrong, bytes.NewReader(buf.Bytes()), false); err == nil {
		t.Error("wrong key must fail")
	}
}
