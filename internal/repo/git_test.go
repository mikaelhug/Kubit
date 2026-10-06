package repo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestStatusReadsGitPorcelainOfANewRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	dir := t.TempDir()
	if _, err := GitInit(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ClusterFile), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := Status(ctx, dir)
	if !st.Repo || st.Branch != "main" || len(st.Changes) != 1 || st.Changes[0].Path != ClusterFile || st.Changes[0].Status != "??" {
		t.Fatalf("status: %+v", st)
	}
}

func TestGitPorcelainPathsAreMadeRelativeToTheServedDir(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	top := t.TempDir()
	if _, err := GitInit(ctx, top); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(top, "clusters", "lab")
	if err := os.MkdirAll(filepath.Join(dir, "apps"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "apps", "a.sops.yaml"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(top, "b.sops.yaml"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := FileStatus(ctx, dir)
	if st["apps/a.sops.yaml"] != "??" || len(st) != 1 {
		t.Errorf("status %v", st)
	}
}
