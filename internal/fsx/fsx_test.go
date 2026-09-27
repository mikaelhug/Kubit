package fsx

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteFileReplacesAndKeepsPerm(t *testing.T) {
	p := filepath.Join(t.TempDir(), "out")
	if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(p, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	fi, _ := os.Stat(p)
	if string(b) != "new" || fi.Mode().Perm() != 0o600 {
		t.Errorf("got %q %v", b, fi.Mode().Perm())
	}
}

func TestWriteStreamFailureLeavesTargetAlone(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "out")
	if err := os.WriteFile(p, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := WriteStream(p, 0o600, func(w io.Writer) error {
		_, _ = w.Write([]byte("partial"))
		return errors.New("boom")
	})
	if err == nil {
		t.Fatal("want error")
	}
	b, _ := os.ReadFile(p)
	entries, _ := os.ReadDir(dir)
	if string(b) != "old" || len(entries) != 1 {
		t.Errorf("target %q, %d entries left", b, len(entries))
	}
}

func TestSweepPartsRemovesOnlyStaleParts(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-2 * time.Hour)
	sub := filepath.Join(dir, "v1.14.0-abc")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]bool{
		filepath.Join(dir, ".kernel.123.part"):        true,
		filepath.Join(sub, ".metal-arm64.iso.9.part"): true,
		filepath.Join(dir, ".fresh.1.part"):           false,
		filepath.Join(dir, "kernel"):                  false,
		filepath.Join(dir, "notes.part"):              false,
	}
	for p, stale := range files {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if stale || filepath.Base(p) != ".fresh.1.part" {
			os.Chtimes(p, old, old)
		}
	}
	if err := SweepParts(dir, time.Hour); err != nil {
		t.Fatal(err)
	}
	for p, stale := range files {
		_, err := os.Stat(p)
		if gone := os.IsNotExist(err); gone != stale {
			t.Errorf("%s: removed=%v, want %v", filepath.Base(p), gone, stale)
		}
	}
	if err := SweepParts(filepath.Join(dir, "missing"), time.Hour); err != nil {
		t.Errorf("a missing directory is not an error: %v", err)
	}
}

func TestWriteOutRegularSpecialAndSymlinkedTargets(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "b.kubitbak")
	if err := WriteOut(p, 0o600, Bytes([]byte("sealed"))); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "sealed" {
		t.Fatalf("regular: %q", b)
	}
	if err := WriteOut(os.DevNull, 0o600, Bytes([]byte("sealed"))); err != nil {
		t.Fatalf("special file: %v", err)
	}
	if fi, err := os.Stat(os.DevNull); err != nil || fi.Mode().IsRegular() {
		t.Fatalf("%s replaced: %v", os.DevNull, err)
	}

	dest := filepath.Join(dir, "dotfiles", "config")
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "config")
	if err := os.Symlink(filepath.Join("dotfiles", "config"), link); err != nil {
		t.Fatal(err)
	}
	if err := WriteOut(link, 0o600, Bytes([]byte("new"))); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the symlink was replaced: %v", err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "new" {
		t.Errorf("symlink target: %q", b)
	}
	if err := WriteOut(dir, 0o600, Bytes([]byte("x"))); err == nil {
		t.Error("a directory is not an output file")
	}
}
