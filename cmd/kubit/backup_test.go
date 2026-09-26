package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteBackupToRegularAndSpecialFiles(t *testing.T) {
	write := func(w io.Writer) error { _, err := io.WriteString(w, "sealed"); return err }
	path := filepath.Join(t.TempDir(), "b.kubitbak")
	if err := writeBackup(path, write); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "sealed" {
		t.Fatalf("got %q", b)
	}
	if err := writeBackup(os.DevNull, write); err != nil {
		t.Fatalf("special file: %v", err)
	}
}
