package repo

import "testing"

func TestNewSecretRefusesTraversalAndNeverOverwrites(t *testing.T) {
	dir, _ := initRepo(t)
	if _, err := NewSecret(dir, "apps/db/secret.sops.yaml", SecretSpec{Name: "db", Namespace: "shop"}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewSecret(dir, "apps/db/secret.sops.yaml", SecretSpec{Name: "db", Namespace: "shop"}); err == nil {
		t.Error("an existing file must not be replaced")
	}
	for _, bad := range []string{"../x.sops.yaml", "/etc/x.sops.yaml", SecretsFile} {
		if _, err := SecretPath(dir, bad); err == nil {
			t.Errorf("%s must be refused", bad)
		}
	}
}
