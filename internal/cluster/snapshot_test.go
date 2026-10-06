package cluster

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"go.etcd.io/bbolt"
)

func etcdDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "etcd.db")
	db, err := bbolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucket([]byte("key"))
		if err != nil {
			return err
		}
		return b.Put([]byte("/registry/a"), []byte("1"))
	})
	if cerr := db.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSnapshotIsEncryptedAtRestAndOnlyItsKeyOpensIt(t *testing.T) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("SOPS_AGE_KEY_FILE", "")
	t.Setenv("SOPS_AGE_KEY", id.String())
	dir := filepath.Join(t.TempDir(), "snapshots")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	plain := etcdDB(t)
	path := filepath.Join(dir, "20261005T120000Z-manual"+snapshotExt)
	if err := encryptSnapshot(plain, path, []string{id.Recipient().String()}, func(Event) {}); err != nil {
		t.Fatal(err)
	}
	enc, _ := os.ReadFile(path)
	if strings.Contains(string(enc), "/registry/a") {
		t.Fatal("the snapshot on disk must be encrypted")
	}
	list, err := listSnapshots(dir)
	if err != nil || len(list) != 1 {
		t.Fatalf("list %+v %v", list, err)
	}
	got, err := openSnapshot(&list[0])
	if err != nil {
		t.Fatal(err)
	}
	want, _ := os.ReadFile(plain)
	if string(got) != string(want) {
		t.Error("the restored bytes differ from the snapshot")
	}
	other, _ := age.GenerateX25519Identity()
	t.Setenv("SOPS_AGE_KEY", other.String())
	if _, err := openSnapshot(&list[0]); err == nil {
		t.Error("another key must not open the snapshot")
	}
}
