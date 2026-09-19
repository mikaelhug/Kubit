package store

import (
	"strings"
	"testing"
)

func TestLockHomeIsExclusive(t *testing.T) {
	dir := t.TempDir()
	l, err := LockHome(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LockHome(dir); err == nil || !strings.Contains(err.Error(), "another kubit serve is running") {
		t.Fatalf("second lock must fail, got %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	l2, err := LockHome(dir)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	l2.Release()
}
