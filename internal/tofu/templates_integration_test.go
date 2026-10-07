//go:build integration

package tofu_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mikaelhug/kubit/internal/config"
	"github.com/mikaelhug/kubit/internal/tofu"
)

func TestTemplatesValidateWithTheLockedProviders(t *testing.T) {
	ctx := context.Background()
	bin, err := tofu.Binary(ctx, filepath.Join(os.TempDir(), "kubit-tofu-bin"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := config.Parse([]byte(decl))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := tofu.Render(dir, c, "/x/kubeconfig", ""); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(os.TempDir(), "kubit-tofu-plugins")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-input=false", "-no-color", "-backend=false", "-lockfile=readonly"},
		{"validate", "-no-color"},
	} {
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "TF_PLUGIN_CACHE_DIR="+cache, "TF_IN_AUTOMATION=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("tofu %s: %v\n%s", args[0], err, out)
		}
	}
}
