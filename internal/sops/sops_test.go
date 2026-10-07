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

func TestEncryptLeaksNoValueOrComment(t *testing.T) {
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

func TestTamperingFailsTheSopsMACAndPathBinding(t *testing.T) {
	id := identity(t)
	enc, err := Encrypt([]byte("a: one\nb: two\n"), Rule{Age: []string{id.Recipient().String()}})
	if err != nil {
		t.Fatal(err)
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

func TestEncryptedRegexNeverLeavesSecretDataInClear(t *testing.T) {
	id := identity(t)
	secret := "apiVersion: v1\nkind: Secret\nmetadata:\n    name: db\nstringData:\n    password: hunter2\n"
	enc, err := Encrypt([]byte(secret), Rule{Age: []string{id.Recipient().String()}, EncryptedRegex: "^(data|stringData)$"})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(enc, []byte("hunter2")) {
		t.Fatalf("stringData is in clear:\n%s", enc)
	}
}

func TestFluxReadsAppSecretsButNeverTheClusterSecrets(t *testing.T) {
	dir := t.TempDir()
	if err := WriteConfig(dir, []string{"age1a", "age1b"}, []string{"age1flux"}, "secrets.sops.yaml"); err != nil {
		t.Fatal(err)
	}
	r, err := RuleFor(dir, filepath.Join(dir, "apps", "db.sops.yaml"))
	if err != nil || strings.Join(r.Age, ",") != "age1a,age1b,age1flux" {
		t.Fatalf("app secrets are readable by Flux: %+v %v", r, err)
	}
	r, err = RuleFor(dir, filepath.Join(dir, "secrets.sops.yaml"))
	if err != nil || strings.Join(r.Age, ",") != "age1a,age1b" {
		t.Fatalf("the cluster's own secrets stay with the operators: %+v %v", r, err)
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

func TestTheSopsCLIPicksTheSameRules(t *testing.T) {
	bin, err := exec.LookPath("sops")
	if err != nil {
		t.Skip("sops not installed")
	}
	operator, flux := identity(t), identity(t)
	dir := t.TempDir()
	if err := WriteConfig(dir, []string{operator.Recipient().String()}, []string{flux.Recipient().String()}, "secrets.sops.yaml"); err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]int{"secrets.sops.yaml": 1, "apps/db/db.sops.yaml": 2} {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("password: hunter2\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, arg := range []string{rel, p} {
			cmd := exec.Command(bin, "-e", arg)
			cmd.Dir, cmd.Env = dir, append(os.Environ(), "SOPS_AGE_KEY="+operator.String(), "SOPS_AGE_KEY_FILE=", "XDG_CONFIG_HOME="+dir)
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("sops -e %s: %v", arg, err)
			}
			got, err := Recipients(out)
			if err != nil || len(got) != want {
				t.Errorf("sops -e %s encrypts to %d recipients, want %d (%v)", arg, len(got), want, err)
			}
		}
	}
}

func TestAddRecipientSeparatesTheClusterSecrets(t *testing.T) {
	dir := t.TempDir()
	old := "# keys of the people who run the cluster\ncreation_rules:\n  - path_regex: \\.sops\\.yaml$\n    age: age1me\n"
	if err := os.WriteFile(filepath.Join(dir, ConfigFile), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := AddRecipient(dir, "age1flux", "secrets.sops.yaml", []string{"apps/example.sops.yaml"})
	if err != nil || !changed {
		t.Fatalf("add: %v %v", changed, err)
	}
	if r, err := RuleFor(dir, filepath.Join(dir, "secrets.sops.yaml")); err != nil || strings.Join(r.Age, ",") != "age1me" {
		t.Errorf("cluster secrets: %+v %v", r, err)
	}
	if r, err := RuleFor(dir, filepath.Join(dir, "apps", "db.sops.yaml")); err != nil || strings.Join(r.Age, ",") != "age1me,age1flux" {
		t.Errorf("app secrets: %+v %v", r, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ConfigFile)); !strings.Contains(string(b), "# keys of the people who run the cluster") {
		t.Errorf("comments survive:\n%s", b)
	}
}
