package cluster

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/mikael/kubit/internal/repo"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/tofu"
	"go.yaml.in/yaml/v4"
)

func TestExportRepoMovesAStoredCluster(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	spec := strings.Replace(convergeSpec, "  extensions:", "  schematicID: abc\n  extensions:", 1)
	if err := st.PutCluster(ctx, store.ClusterRow{Name: "lab", Spec: []byte(spec), State: StateReady}); err != nil {
		t.Fatal(err)
	}
	raw, _ := yaml.Marshal(bundle(t))
	if err := st.PutClusterSecrets(ctx, "lab", store.ClusterSecrets{SecretsBundle: raw, Talosconfig: []byte("t"), Kubeconfig: []byte("k")}); err != nil {
		t.Fatal(err)
	}
	flux, _ := age.GenerateX25519Identity()
	if err := st.PutSOPSKey(ctx, "lab", keysFile(flux), flux.Recipient().String()); err != nil {
		t.Fatal(err)
	}
	m := NewManager(st, t.TempDir())
	me, _ := age.GenerateX25519Identity()
	dir := filepath.Join(t.TempDir(), "lab")
	r, moved, err := m.ExportRepo(ctx, "lab", dir, []string{me.Recipient().String()})
	if err != nil || moved {
		t.Fatalf("export: %v moved=%v", err, moved)
	}
	loaded, err := repo.LoadWith(dir, []age.Identity{me})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(loaded.Secrets.BundleYAML, raw) && loaded.Secrets.Bundle.Cluster.ID != r.Secrets.Bundle.Cluster.ID {
		t.Error("the bundle must survive the move")
	}
	if loaded.Secrets.FluxRecipient() != flux.Recipient().String() {
		t.Error("the Flux key must move with the cluster")
	}
	if loaded.Cluster.Spec.SchematicID != "" || len(loaded.Cluster.Spec.Nodes) != 3 {
		t.Errorf("declaration %+v", loaded.Cluster.Spec)
	}
	d, err := FromRepo(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.checkCachedSecrets(ctx, d); err != nil {
		t.Errorf("the cache must accept its own repo: %v", err)
	}
	if _, _, err := m.ExportRepo(ctx, "lab", dir, []string{me.Recipient().String()}); err == nil {
		t.Error("a second export must not overwrite the repo")
	}
}

func TestMoveStateEncryptsIt(t *testing.T) {
	bin := filepath.Join(os.Getenv("HOME"), ".kubit", "bin", "tofu-"+tofu.Version)
	if _, err := os.Stat(bin); err != nil {
		t.Skip("no cached tofu binary")
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(bin, filepath.Join(home, "bin", "tofu-"+tofu.Version)); err != nil {
		t.Fatal(err)
	}
	m := NewManager(testStore(t), home)
	dir := m.platformDir("lab")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	tf := "terraform {\n  backend \"local\" {}\n}\nresource \"terraform_data\" \"x\" {\n  input = \"plain-secret\"\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(tf), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-input=false"}, {"apply", "-input=false", "-auto-approve"}} {
		cmd := exec.Command(bin, args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("tofu %v: %v\n%s", args, err, out)
		}
	}
	target := filepath.Join(t.TempDir(), "repo", "state", "platform.tfstate")
	moved, err := m.moveState(context.Background(), "lab", target, "abcdefghijklmnopqrstuvwxyz012345")
	if err != nil || !moved {
		t.Fatalf("move: %v %v", moved, err)
	}
	b, _ := os.ReadFile(target)
	if bytes.Contains(b, []byte("plain-secret")) || !bytes.Contains(b, []byte("encrypted_data")) {
		t.Errorf("state not encrypted:\n%.300s", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "terraform.tfstate.moved")); err != nil {
		t.Error("the old state is kept aside")
	}
}
