package repo

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/mikael/kubit/internal/sops"
)

func TestSecretFilesAndNewSecret(t *testing.T) {
	dir, id := initRepo(t)
	if err := NewSecret(dir, "apps/db/secret.sops.yaml", "db", "shop"); err != nil {
		t.Fatal(err)
	}
	if err := NewSecret(dir, "apps/db/secret.sops.yaml", "db", "shop"); err == nil {
		t.Error("an existing file must not be replaced")
	}
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "x.sops.yaml"), []byte("a: b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := SecretFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("files %+v", files)
	}
	f := files[0]
	if f.Path != "apps/db/secret.sops.yaml" || f.Kind != "Secret" || f.Name != "db" || f.Namespace != "shop" || f.Recipients[0] != id.Recipient().String() || f.Error != "" {
		t.Errorf("file %+v", f)
	}
	b, _ := os.ReadFile(filepath.Join(dir, f.Path))
	if r, _ := sops.RuleOf(b); r.EncryptedRegex != "^(data|stringData)$" {
		t.Errorf("a new Secret encrypts only its data: %+v", r)
	}
	if _, err := sops.Decrypt(b, []age.Identity{id}); err != nil {
		t.Errorf("decrypt the new file: %v", err)
	}
	for _, bad := range []string{"../x.sops.yaml", "/etc/x.sops.yaml", "apps/plain.yaml", SecretsFile, ".git/x.sops.yaml", "state/a.sops.yaml"} {
		if _, err := SecretPath(dir, bad); err == nil {
			t.Errorf("%s must be refused", bad)
		}
	}
}

func TestWatchReportsChangesOnce(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	got := make(chan string, 8)
	if err := Watch(ctx, []string{dir}, func(d string) { got <- d }); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "apps")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	for range 3 {
		if err := os.WriteFile(filepath.Join(sub, "a.sops.yaml"), []byte(strings.Repeat("x", 3)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case d := <-got:
		if d != dir {
			t.Errorf("changed %s", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no change reported")
	}
	select {
	case <-got:
		t.Error("a burst must settle into one report")
	case <-time.After(2 * settle):
	}
}
