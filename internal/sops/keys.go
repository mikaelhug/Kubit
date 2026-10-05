package sops

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
)

func DefaultKeyFile() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "sops", "age", "keys.txt")
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return filepath.Join("~", ".config", "sops", "age", "keys.txt")
	}
	return filepath.Join(dir, "sops", "age", "keys.txt")
}

func Identities() ([]age.Identity, error) {
	var out []age.Identity
	add := func(src string, b []byte) error {
		ids, err := age.ParseIdentities(bytes.NewReader(b))
		if err != nil {
			return fmt.Errorf("%s: %w", src, err)
		}
		out = append(out, ids...)
		return nil
	}
	if k := os.Getenv("SOPS_AGE_KEY"); k != "" {
		if err := add("SOPS_AGE_KEY", []byte(k)); err != nil {
			return nil, err
		}
	}
	files := []string{os.Getenv("SOPS_AGE_KEY_FILE"), DefaultKeyFile()}
	if home, err := os.UserHomeDir(); err == nil {
		files = append(files, filepath.Join(home, ".config", "sops", "age", "keys.txt"))
	}
	seen := map[string]bool{}
	for _, f := range files {
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		b, err := os.ReadFile(f)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if err := add(f, b); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func Recipient(id age.Identity) string {
	if x, ok := id.(*age.X25519Identity); ok {
		return x.Recipient().String()
	}
	return ""
}

func KeyFileFor(id *age.X25519Identity, created string) []byte {
	return fmt.Appendf(nil, "# created: %s\n# public key: %s\n%s\n", created, id.Recipient(), id)
}

func ParseRecipients(s string) []string {
	var out []string
	for _, r := range strings.FieldsFunc(s, func(c rune) bool { return c == ',' || c == ' ' || c == '\n' }) {
		if r = strings.TrimSpace(r); r != "" {
			out = append(out, r)
		}
	}
	return out
}
