package repo

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mikael/kubit/internal/sops"
	"go.yaml.in/yaml/v4"
)

type SecretFile struct {
	Path       string     `json:"path"`
	Recipients []string   `json:"recipients"`
	Keys       []sops.Key `json:"keys"`
	Kind       string     `json:"kind,omitempty"`
	Name       string     `json:"name,omitempty"`
	Namespace  string     `json:"namespace,omitempty"`
	Error      string     `json:"error,omitempty"`
}

var skipDirs = []string{".git", ".terraform", StateDir, "node_modules"}

func IsSecretFile(rel string) bool {
	base := filepath.Base(rel)
	return base != sops.ConfigFile && (strings.HasSuffix(base, ".sops.yaml") || strings.HasSuffix(base, ".sops.yml")) && filepath.ToSlash(rel) != SecretsFile
}

func SecretFiles(dir string) ([]SecretFile, error) {
	var out []SecretFile
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != dir && slices.Contains(skipDirs, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil || !IsSecretFile(rel) {
			return err
		}
		out = append(out, describe(p, filepath.ToSlash(rel)))
		return nil
	})
	return out, err
}

func describe(path, rel string) SecretFile {
	f := SecretFile{Path: rel, Recipients: []string{}, Keys: []sops.Key{}}
	b, err := os.ReadFile(path)
	if err == nil && !sops.Encrypted(b) {
		err = errors.New("not encrypted")
	}
	if err == nil {
		f.Recipients, err = sops.Recipients(b)
	}
	if err == nil {
		f.Keys, err = sops.Keys(b)
	}
	if err != nil {
		f.Error = err.Error()
		return f
	}
	var head struct {
		Kind     string `yaml:"kind"`
		Metadata struct {
			Name      string `yaml:"name"`
			Namespace string `yaml:"namespace"`
		} `yaml:"metadata"`
	}
	if yaml.Unmarshal(b, &head) == nil {
		f.Kind, f.Name, f.Namespace = plainValue(head.Kind), plainValue(head.Metadata.Name), plainValue(head.Metadata.Namespace)
	}
	return f
}

func plainValue(v string) string {
	if strings.HasPrefix(v, "ENC[") {
		return ""
	}
	return v
}

func SecretPath(dir, rel string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(rel))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || !IsSecretFile(clean) {
		return "", fmt.Errorf("%s: not a SOPS file in the repo", rel)
	}
	for _, part := range strings.Split(filepath.ToSlash(clean), "/") {
		if slices.Contains(skipDirs, part) {
			return "", fmt.Errorf("%s: not a SOPS file in the repo", rel)
		}
	}
	return filepath.Join(dir, clean), nil
}

func NewSecret(dir, rel, name, namespace string) error {
	path, err := SecretPath(dir, rel)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", rel)
	}
	rule, err := sops.RuleFor(dir, path)
	if err != nil {
		return err
	}
	meta := map[string]string{"name": name}
	if namespace != "" {
		meta["namespace"] = namespace
	}
	plain, err := yaml.Marshal(map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": meta, "type": "Opaque", "stringData": map[string]string{}})
	if err != nil {
		return err
	}
	if rule.EncryptedRegex == "" && rule.UnencryptedRegex == "" && rule.EncryptedSuffix == "" && rule.UnencryptedSuffix == "" {
		rule.EncryptedRegex = "^(data|stringData)$"
	}
	enc, err := sops.Encrypt(plain, rule)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, enc, 0o644)
}
