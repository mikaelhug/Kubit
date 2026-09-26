package tofu

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const showJSON = `{"values":{"root_module":{"resources":[{"address":"helm_release.metallb[0]","type":"helm_release","values":{"name":"metallb","namespace":"metallb-system","chart":"metallb","version":"0.16.1","status":"deployed"}}]}}}`

func TestReleasesAreCachedUntilTheStateChanges(t *testing.T) {
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	bin := filepath.Join(dir, "tofu")
	script := "#!/bin/sh\necho x >> " + calls + "\ncat <<'EOF'\n" + showJSON + "\nEOF\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(dir, "terraform.tfstate")
	if err := os.WriteFile(state, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &Runner{Bin: bin, Dir: dir}
	ctx := context.Background()
	count := func() int {
		b, _ := os.ReadFile(calls)
		return strings.Count(string(b), "x")
	}
	first, err := r.Releases(ctx)
	if err != nil || len(first) != 1 || first[0].Addon != "metallb" {
		t.Fatalf("releases: %+v %v", first, err)
	}
	first[0].Status = "mutated"
	again, _ := r.Releases(ctx)
	if count() != 1 {
		t.Errorf("an unchanged state must not run tofu again: %d runs", count())
	}
	if again[0].Status != "deployed" {
		t.Error("callers must get their own copy")
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(state, later, later); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Releases(ctx); err != nil {
		t.Fatal(err)
	}
	if count() != 2 {
		t.Errorf("a newer state must be read again: %d runs", count())
	}
	if err := os.Remove(state); err != nil {
		t.Fatal(err)
	}
	if none, _ := r.Releases(ctx); len(none) != 0 || count() != 2 {
		t.Errorf("no state, no releases: %+v", none)
	}
}
