package sops

import (
	"strings"
	"testing"

	"filippo.io/age"
	"go.yaml.in/yaml/v4"
)

func TestEditKeepsTheRuleAndRecipients(t *testing.T) {
	id := identity(t)
	other := identity(t)
	secret := "apiVersion: v1\nkind: Secret\nmetadata:\n    name: db\nstringData:\n    password: hunter2\n    user: app\n"
	rule := Rule{Age: []string{id.Recipient().String(), other.Recipient().String()}, EncryptedRegex: "^(data|stringData)$"}
	enc, err := Encrypt([]byte(secret), rule)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := Keys(enc)
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, k := range keys {
		listed = append(listed, strings.Join(k.Path, ".")+map[bool]string{true: "*", false: ""}[k.Encrypted])
	}
	if got := strings.Join(listed, " "); got != "apiVersion kind metadata.name stringData.password* stringData.user*" {
		t.Errorf("keys %s", got)
	}
	edited, err := Edit(enc, []age.Identity{id}, func(root *yaml.Node) error {
		if err := Set(root, []string{"stringData", "password"}, "s3cret\nline2"); err != nil {
			return err
		}
		if err := Set(root, []string{"stringData", "token"}, "abc"); err != nil {
			return err
		}
		return Delete(root, []string{"stringData", "user"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(edited), "s3cret") || strings.Contains(string(edited), "abc") {
		t.Fatalf("edited values leaked:\n%s", edited)
	}
	if r, _ := RuleOf(edited); r.EncryptedRegex != rule.EncryptedRegex || len(r.Age) != 2 {
		t.Errorf("rule after edit %+v", r)
	}
	for path, want := range map[string]string{"stringData.password": "s3cret\nline2", "stringData.token": "abc", "metadata.name": "db"} {
		got, err := Get(edited, []age.Identity{other}, strings.Split(path, "."))
		if err != nil || got != want {
			t.Errorf("%s = %q %v", path, got, err)
		}
	}
	if _, err := Get(edited, []age.Identity{id}, []string{"stringData", "user"}); err == nil {
		t.Error("a deleted key must be gone")
	}
	if _, err := Edit(enc, []age.Identity{id}, func(root *yaml.Node) error { return Set(root, []string{"metadata", "name", "x"}, "y") }); err == nil {
		t.Error("setting below a scalar must fail")
	}
}
