package sops

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"go.yaml.in/yaml/v4"
)

const sample = `# a comment that must not leak
name: web
replicas: 3
ratio: 0.5
enabled: true
empty: ""
password_unencrypted: visible
tls:
    crt: |
        line one
        line two
    hosts:
        - a.example
        - b.example
`

func identity(t *testing.T) *age.X25519Identity {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func decode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := yaml.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestRoundTrip(t *testing.T) {
	id := identity(t)
	enc, err := Encrypt([]byte(sample), Rule{Age: []string{id.Recipient().String()}})
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"web", "line one", "a.example", "comment", "0.5"} {
		if bytes.Contains(enc, []byte(leak)) {
			t.Errorf("ciphertext leaks %q:\n%s", leak, enc)
		}
	}
	if !bytes.Contains(enc, []byte("password_unencrypted: visible")) {
		t.Error("the _unencrypted suffix must stay readable")
	}
	if !Encrypted(enc) || Encrypted([]byte(sample)) {
		t.Error("Encrypted misreads the files")
	}
	if r, _ := Recipients(enc); len(r) != 1 || r[0] != id.Recipient().String() {
		t.Errorf("recipients %v", r)
	}
	plain, err := Decrypt(enc, []age.Identity{id})
	if err != nil {
		t.Fatal(err)
	}
	got, want := decode(t, plain), decode(t, []byte(sample))
	if !equal(got, want) {
		t.Errorf("round trip:\n%v\nwant\n%v", got, want)
	}
}

func equal(a, b map[string]any) bool {
	x, _ := yaml.Marshal(a)
	y, _ := yaml.Marshal(b)
	return bytes.Equal(x, y)
}

func TestTamperAndWrongKey(t *testing.T) {
	id := identity(t)
	enc, err := Encrypt([]byte("a: one\nb: two\n"), Rule{Age: []string{id.Recipient().String()}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decrypt(enc, []age.Identity{identity(t)}); err == nil || !strings.Contains(err.Error(), "none of your age keys") {
		t.Errorf("wrong key: %v", err)
	}
	lines := strings.Split(string(enc), "\n")
	var a, b int
	for i, l := range lines {
		if strings.HasPrefix(l, "a: ") {
			a = i
		}
		if strings.HasPrefix(l, "b: ") {
			b = i
		}
	}
	swapped := append([]string(nil), lines...)
	swapped[a], swapped[b] = "a: "+strings.TrimPrefix(lines[b], "b: "), "b: "+strings.TrimPrefix(lines[a], "a: ")
	if _, err := Decrypt([]byte(strings.Join(swapped, "\n")), []age.Identity{id}); err == nil {
		t.Error("values moved between keys must not decrypt")
	}
	dropped := strings.Replace(string(enc), lines[b]+"\n", "", 1)
	if _, err := Decrypt([]byte(dropped), []age.Identity{id}); !errors.Is(err, ErrMAC) {
		t.Errorf("a removed value must fail the MAC: %v", err)
	}
}

func TestEncryptedRegexLeavesTheRestPlain(t *testing.T) {
	id := identity(t)
	secret := "apiVersion: v1\nkind: Secret\nmetadata:\n    name: db\nstringData:\n    password: hunter2\n"
	enc, err := Encrypt([]byte(secret), Rule{Age: []string{id.Recipient().String()}, EncryptedRegex: "^(data|stringData)$"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(enc, []byte("name: db")) || bytes.Contains(enc, []byte("hunter2")) {
		t.Fatalf("only stringData is encrypted:\n%s", enc)
	}
	plain, err := Decrypt(enc, []age.Identity{id})
	if err != nil || !bytes.Contains(plain, []byte("password: hunter2")) {
		t.Fatalf("decrypt: %v\n%s", err, plain)
	}
}

func TestRuleFor(t *testing.T) {
	dir := t.TempDir()
	if err := WriteConfig(dir, []string{"age1a", "age1b"}); err != nil {
		t.Fatal(err)
	}
	r, err := RuleFor(dir, filepath.Join(dir, "apps", "db.sops.yaml"))
	if err != nil || strings.Join(r.Age, ",") != "age1a,age1b" {
		t.Fatalf("rule %+v %v", r, err)
	}
	if _, err := RuleFor(dir, filepath.Join(dir, "cluster.yaml")); err == nil {
		t.Error("a plain file must not match")
	}
}

func TestCompatibleWithTheSopsCLI(t *testing.T) {
	bin, err := exec.LookPath("sops")
	if err != nil {
		t.Skip("sops not installed")
	}
	id := identity(t)
	dir := t.TempDir()
	env := append(os.Environ(), "SOPS_AGE_KEY="+id.String(), "SOPS_AGE_KEY_FILE=", "XDG_CONFIG_HOME="+dir)
	run := func(stdin []byte, args ...string) []byte {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env, cmd.Dir, cmd.Stdin = env, dir, bytes.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("sops %v: %v\n%s", args, err, out)
		}
		return out
	}
	ours, err := Encrypt([]byte(sample), Rule{Age: []string{id.Recipient().String()}})
	if err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "ours.sops.yaml")
	if err := os.WriteFile(f, ours, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := decode(t, run(nil, "-d", f)); !equal(got, decode(t, []byte(sample))) {
		t.Errorf("sops -d of our file: %v", got)
	}
	plain := filepath.Join(dir, "theirs.yaml")
	if err := os.WriteFile(plain, []byte(sample), 0o600); err != nil {
		t.Fatal(err)
	}
	theirs := run(nil, "-e", "--age", id.Recipient().String(), plain)
	got, err := Decrypt(theirs, []age.Identity{id})
	if err != nil {
		t.Fatalf("decrypt sops output: %v\n%s", err, theirs)
	}
	if !equal(decode(t, got), decode(t, []byte(sample))) {
		t.Errorf("our decrypt of sops output:\n%s", got)
	}
	theirs = run(nil, "-e", "--age", id.Recipient().String(), "--encrypted-regex", "^(tls|name)$", plain)
	if got, err = Decrypt(theirs, []age.Identity{id}); err != nil || !equal(decode(t, got), decode(t, []byte(sample))) {
		t.Errorf("encrypted_regex file: %v\n%s", err, got)
	}
}
