package sops

import (
	"strings"
	"testing"

	"filippo.io/age"
	"go.yaml.in/yaml/v4"
)

func TestEditNeverWritesValuesInClearAndKeepsTheRule(t *testing.T) {
	id := identity(t)
	other := identity(t)
	secret := "apiVersion: v1\nkind: Secret\nmetadata:\n    name: db\nstringData:\n    password: hunter2\n    user: app\n"
	rule := Rule{Age: []string{id.Recipient().String(), other.Recipient().String()}, EncryptedRegex: "^(data|stringData)$"}
	enc, err := Encrypt([]byte(secret), rule)
	if err != nil {
		t.Fatal(err)
	}
	edited, err := Edit(enc, []age.Identity{id}, func(root *yaml.Node) error {
		if err := Set(root, []string{"stringData", "password"}, "s3cret\nline2"); err != nil {
			return err
		}
		return Set(root, []string{"stringData", "token"}, "abc")
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
	values, err := Values(edited, []age.Identity{other})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range values {
		got[strings.Join(e.Path, ".")] = e.Value
	}
	for path, want := range map[string]string{"stringData.password": "s3cret\nline2", "stringData.token": "abc"} {
		if got[path] != want {
			t.Errorf("%s = %q", path, got[path])
		}
	}
}
