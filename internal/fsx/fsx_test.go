package fsx

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteFileTightensThePermOfAnExistingFile(t *testing.T) {
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
	files := map[string]bool{
		filepath.Join(dir, ".kernel.123.part"): true,
		filepath.Join(dir, ".fresh.1.part"):    false,
		filepath.Join(dir, "kernel"):           false,
		filepath.Join(dir, "notes.part"):       false,
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
}
