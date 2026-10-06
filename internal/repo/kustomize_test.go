package repo

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/mikael/kubit/internal/sops"
	"go.yaml.in/yaml/v4"
)

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAppliedFollowsFluxKustomizeRules(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "flux/shop/db.sops.yaml", "a: b\n")
	write(t, dir, "flux/loose/x.sops.yaml", "a: b\n")
	write(t, dir, "other/y.sops.yaml", "a: b\n")
	for rel, want := range map[string]string{
		"flux/shop/db.sops.yaml": "",
		"other/y.sops.yaml":      "outside flux",
	} {
		if got := Applied(dir, "./flux", rel); got != want {
			t.Errorf("generated: %s: %q, want %q", rel, got, want)
		}
	}
	write(t, dir, "flux/kustomization.yaml", "resources:\n  - ./shop\n")
	write(t, dir, "flux/shop/kustomization.yaml", "# keep\nresources:\n  - deploy.yaml\n")
	for rel, want := range map[string]string{
		"flux/shop/db.sops.yaml": "not in flux/shop/kustomization.yaml",
		"flux/loose/x.sops.yaml": "not in flux/kustomization.yaml",
	} {
		if got := Applied(dir, "flux/", rel); got != want {
			t.Errorf("explicit: %s: %q, want %q", rel, got, want)
		}
	}
	k, err := ListResource(dir, "flux/shop/db.sops.yaml")
	if err != nil || k != "flux/shop/kustomization.yaml" {
		t.Fatalf("list: %q %v", k, err)
	}
	if got := Applied(dir, "flux", "flux/shop/db.sops.yaml"); got != "" {
		t.Errorf("listed: %q", got)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "flux/shop/kustomization.yaml"))
	if !strings.Contains(string(b), "# keep") || !strings.Contains(string(b), "- db.sops.yaml") {
		t.Errorf("kustomization:\n%s", b)
	}
	write(t, dir, "flux/kustomization.yaml", "resources:\n  - ./shop\n  - loose\n")
	if got := Applied(dir, "flux", "flux/loose/x.sops.yaml"); got != "flux/loose has no kustomization.yaml" {
		t.Errorf("a listed directory needs its own kustomization: %q", got)
	}
}

func TestValidateSecretMirrorsKubernetesValidation(t *testing.T) {
	for doc, want := range map[string]string{
		"kind: Secret\nmetadata: {name: db}\nstringData: {password: x}\n":                          "",
		"kind: Secret\nmetadata: {name: Db_1}\n":                                                   "name",
		"kind: Secret\nmetadata: {name: db}\nstringData: {\"a b\": x}\n":                           "key",
		"kind: Secret\nmetadata: {name: tls}\ntype: kubernetes.io/tls\nstringData: {tls.crt: x}\n": "tls.key",
	} {
		var n yaml.Node
		if err := yaml.Unmarshal([]byte(doc), &n); err != nil {
			t.Fatal(err)
		}
		err := ValidateSecret(n.Content[0])
		switch {
		case want == "" && err != nil:
			t.Errorf("%q: %v", doc, err)
		case want != "" && (err == nil || !strings.Contains(err.Error(), want)):
			t.Errorf("%q: %v, want %q", doc, err, want)
		}
	}
}

func TestSecretEditsRefuseAStaleOrInvalidFile(t *testing.T) {
	dir, id := initRepo(t)
	ids := []age.Identity{id}
	if _, err := NewSecret(dir, "apps/db.sops.yaml", SecretSpec{Name: "db", Namespace: "shop", Type: TypeTLS, StringData: map[string]string{"tls.crt": "c", "tls.key": "k"}}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "apps", "db.sops.yaml")
	b, _ := os.ReadFile(path)
	if err := EditSecret(path, "stale", ids, func(*yaml.Node) error { return nil }); err != ErrStale {
		t.Errorf("stale: %v", err)
	}
	err := EditSecret(path, Fingerprint(b), ids, func(root *yaml.Node) error {
		return sops.Delete(root, []string{"stringData", "tls.key"})
	})
	if err == nil || !strings.Contains(err.Error(), "tls.key") {
		t.Errorf("an edit that breaks the type is refused: %v", err)
	}
	if _, err := DeleteSecret(dir, "apps/db.sops.yaml", "stale"); err != ErrStale {
		t.Errorf("stale delete: %v", err)
	}
}

func TestMoveReencryptsForTheNewRule(t *testing.T) {
	dir, id := initRepo(t)
	other, _ := age.GenerateX25519Identity()
	rules := "creation_rules:\n  - path_regex: ^team/\n    age: " + id.Recipient().String() + "," + other.Recipient().String() + "\n  - path_regex: \\.sops\\.yaml$\n    age: " + id.Recipient().String() + "\n"
	write(t, dir, sops.ConfigFile, rules)
	if _, err := NewSecret(dir, "apps/a.sops.yaml", SecretSpec{Name: "a"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "apps", "a.sops.yaml"))
	if _, err := MoveSecret(dir, "apps/a.sops.yaml", "team/a.sops.yaml", Fingerprint(b), []age.Identity{id}); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(dir, "team", "a.sops.yaml"))
	if rs, _ := sops.Recipients(b); !slices.Contains(rs, other.Recipient().String()) {
		t.Errorf("recipients follow the new path's rule: %v", rs)
	}
}
