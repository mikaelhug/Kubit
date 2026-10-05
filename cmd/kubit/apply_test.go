package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/repo"
	"github.com/mikael/kubit/internal/store"
)

func TestPlanReportsNodesThatCannotBeReached(t *testing.T) {
	t.Setenv("KUBIT_HOME", t.TempDir())
	t.Setenv(store.EnvMasterKey, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{5}, 32)))
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SOPS_AGE_KEY", id.String())
	t.Setenv("SOPS_AGE_KEY_FILE", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	c, err := config.Parse([]byte("apiVersion: kubit.dev/v1\nkind: Cluster\nmetadata: {name: lab}\nspec:\n  nodes:\n    - {hostname: cp-01, ip: 127.0.0.1, role: controlplane, installDisk: {path: /dev/sda}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "lab")
	if _, err := repo.Init(dir, c, []string{id.Recipient().String()}); err != nil {
		t.Fatal(err)
	}
	cmd := planCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{dir})
	cmd.SetContext(t.Context())
	err = cmd.Execute()
	var code exitCode
	if !errors.As(err, &code) || code != 1 {
		t.Fatalf("exit %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "! not in maintenance mode: cp-01 (127.0.0.1)") || !strings.Contains(out.String(), "lab cannot be applied.") {
		t.Errorf("output:\n%s", out.String())
	}
}
