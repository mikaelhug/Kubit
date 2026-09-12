//go:build integration

package tofu_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/mikael/kubit/internal/tofu"
)

func TestBinaryDownloads(t *testing.T) {
	p, err := tofu.Binary(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(p, "version").Output()
	if err != nil || !strings.Contains(string(out), tofu.Version) {
		t.Fatalf("%s version: %q %v", p, out, err)
	}
}
