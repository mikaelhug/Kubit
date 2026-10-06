package tofu

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeTofu(t *testing.T) (bin, calls string) {
	t.Helper()
	dir := t.TempDir()
	calls = filepath.Join(dir, "calls")
	bin = filepath.Join(dir, "tofu")
	script := `#!/bin/sh
{
  printf '%s' "$*"
  case "$TF_ENCRYPTION" in *unencrypted*) printf ' [fallback]' ;; esac
  echo
} >> ` + calls + `
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, calls
}

func lines(t *testing.T, path string) []string {
	t.Helper()
	b, _ := os.ReadFile(path)
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func TestRunnerNeverReadsStateInClearAndRemovesItsCredentials(t *testing.T) {
	bin, calls := fakeTofu(t)
	cache := filepath.Join(t.TempDir(), "plugins")
	r, err := New(bin, cache, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Passphrase = "p"
	if err := r.WriteKubeconfig([]byte("kind: Config")); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := r.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Plan(ctx); err != nil {
		t.Fatal(err)
	}
	if got := lines(t, calls); strings.Contains(strings.Join(got, "\n"), "[fallback]") {
		t.Errorf("the state is never read unencrypted: %v", got)
	}
	if fi, err := os.Stat(filepath.Join(r.Dir, "kubeconfig")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("kubeconfig must be private: %v", err)
	}
	dir := r.Dir
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("the workspace is removed after the run")
	}
}
