package repo

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"filippo.io/age"
	"github.com/mikael/kubit/internal/sops"
)

func TestLetFluxDecryptRekeysAppSecretsOnly(t *testing.T) {
	me, _ := age.GenerateX25519Identity()
	flux, _ := age.GenerateX25519Identity()
	dir := t.TempDir()
	old := "creation_rules:\n  - path_regex: \\.sops\\.yaml$\n    age: " + me.Recipient().String() + "\n"
	if err := os.WriteFile(filepath.Join(dir, sops.ConfigFile), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewSecret(dir, "apps/db/db.sops.yaml", SecretSpec{Name: "db", Namespace: "shop"}); err != nil {
		t.Fatal(err)
	}
	n, err := LetFluxDecrypt(dir, flux.Recipient().String(), []age.Identity{me})
	if err != nil || n != 1 {
		t.Fatalf("rekeyed %d: %v", n, err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "apps", "db", "db.sops.yaml"))
	if rs, _ := sops.Recipients(b); !slices.Contains(rs, flux.Recipient().String()) {
		t.Errorf("the app secret is readable by Flux: %v", rs)
	}
	if r, _ := sops.RuleFor(dir, filepath.Join(dir, SecretsFile)); slices.Contains(r.Age, flux.Recipient().String()) {
		t.Error("the cluster's own secrets never include Flux")
	}
}
