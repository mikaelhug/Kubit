package repo

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"filippo.io/age"
	"github.com/mikaelhug/kubit/internal/fsx"
	"github.com/mikaelhug/kubit/internal/sops"
	"go.yaml.in/yaml/v4"
)

type SecretFile struct {
	Path       string     `json:"path"`
	Recipients []string   `json:"recipients"`
	Keys       []sops.Key `json:"keys"`
	Kind       string     `json:"kind,omitempty"`
	Type       string     `json:"type,omitempty"`
	Name       string     `json:"name,omitempty"`
	Namespace  string     `json:"namespace,omitempty"`
	Error      string     `json:"error,omitempty"`
	Hash       string     `json:"hash"`
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
	f.Hash = Fingerprint(b)
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
		Type     string `yaml:"type"`
		Metadata struct {
			Name      string `yaml:"name"`
			Namespace string `yaml:"namespace"`
		} `yaml:"metadata"`
	}
	if yaml.Unmarshal(b, &head) == nil {
		f.Kind, f.Type, f.Name, f.Namespace = plainValue(head.Kind), plainValue(head.Type), plainValue(head.Metadata.Name), plainValue(head.Metadata.Namespace)
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

var (
	dns1123Subdomain = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)
	dns1123Label     = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	secretKeyRe      = regexp.MustCompile(`^[-._a-zA-Z0-9]+$`)
)

const (
	TypeOpaque     = "Opaque"
	TypeTLS        = "kubernetes.io/tls"
	TypeDockerJSON = "kubernetes.io/dockerconfigjson"
)

type SecretSpec struct {
	Name       string            `json:"name"`
	Namespace  string            `json:"namespace"`
	Type       string            `json:"type"`
	StringData map[string]string `json:"stringData"`
}

func NewSecret(dir, rel string, spec SecretSpec) ([]string, error) {
	path, err := SecretPath(dir, rel)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("%s already exists", rel)
	}
	rule, err := sops.RuleFor(dir, path)
	if err != nil {
		return nil, err
	}
	if spec.Type == "" {
		spec.Type = TypeOpaque
	}
	if spec.StringData == nil {
		spec.StringData = map[string]string{}
	}
	if spec.Namespace == "" {
		spec.Namespace = "default"
	}
	meta := map[string]string{"name": spec.Name, "namespace": spec.Namespace}
	plain, err := yaml.Marshal(map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": meta, "type": spec.Type, "stringData": spec.StringData})
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(plain, &doc); err != nil {
		return nil, err
	}
	if err := ValidateSecret(doc.Content[0]); err != nil {
		return nil, err
	}
	if rule.EncryptedRegex == "" && rule.UnencryptedRegex == "" && rule.EncryptedSuffix == "" && rule.UnencryptedSuffix == "" {
		rule.EncryptedRegex = "^(data|stringData)$"
	}
	enc, err := sops.Encrypt(plain, rule)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := create(path, 0o644, enc); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("%s already exists", rel)
		}
		return nil, err
	}
	return ListPath(dir, rel)
}

func ValidateSecret(root *yaml.Node) error {
	var s struct {
		Kind     string `yaml:"kind"`
		Type     string `yaml:"type"`
		Metadata struct {
			Name      string `yaml:"name"`
			Namespace string `yaml:"namespace"`
		} `yaml:"metadata"`
		Data       map[string]string `yaml:"data"`
		StringData map[string]string `yaml:"stringData"`
	}
	if err := root.Decode(&s); err != nil {
		return err
	}
	if s.Kind != "Secret" {
		return nil
	}
	var errs []error
	if len(s.Metadata.Name) > 253 || !dns1123Subdomain.MatchString(s.Metadata.Name) {
		errs = append(errs, fmt.Errorf("name %q: lowercase letters, digits, '-' and '.'", s.Metadata.Name))
	}
	if s.Metadata.Namespace != "" && (len(s.Metadata.Namespace) > 63 || !dns1123Label.MatchString(s.Metadata.Namespace)) {
		errs = append(errs, fmt.Errorf("namespace %q: lowercase letters, digits and '-'", s.Metadata.Namespace))
	}
	values := map[string]string{}
	for k, v := range s.Data {
		b, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("data.%s is not base64", k))
		}
		values[k] = string(b)
	}
	for k, v := range s.StringData {
		values[k] = v
	}
	for k := range values {
		if len(k) > 253 || !secretKeyRe.MatchString(k) {
			errs = append(errs, fmt.Errorf("key %q: letters, digits, '-', '_' and '.'", k))
		}
	}
	switch s.Type {
	case TypeTLS:
		for _, k := range []string{"tls.crt", "tls.key"} {
			if strings.TrimSpace(values[k]) == "" {
				errs = append(errs, fmt.Errorf("a TLS secret needs %s", k))
			}
		}
	case TypeDockerJSON:
		var v map[string]any
		if err := json.Unmarshal([]byte(values[".dockerconfigjson"]), &v); err != nil {
			errs = append(errs, errors.New("a registry secret needs .dockerconfigjson as JSON"))
		}
	}
	return errors.Join(errs...)
}

func EditSecret(path, hash string, ids []age.Identity, edit func(root *yaml.Node) error) error {
	if err := requireHash(hash); err != nil {
		return err
	}
	return replace(path, hash, 0o644, func(b []byte) ([]byte, error) {
		return sops.Edit(b, ids, func(root *yaml.Node) error {
			if err := edit(root); err != nil {
				return err
			}
			return ValidateSecret(root)
		})
	})
}

func DeleteSecret(dir, rel, hash string) ([]string, error) {
	if err := requireHash(hash); err != nil {
		return nil, err
	}
	path, err := SecretPath(dir, rel)
	if err != nil {
		return nil, err
	}
	unlock := fsx.Lock(path)
	defer unlock()
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if hash != Fingerprint(b) {
		return nil, ErrStale
	}
	if err := os.Remove(path); err != nil {
		return nil, err
	}
	touched, err := UnlistPath(dir, rel)
	pruneEmpty(dir, filepath.Dir(path))
	return touched, err
}

func pruneEmpty(root, dir string) {
	for dir != root && strings.HasPrefix(dir, root+string(filepath.Separator)) && os.Remove(dir) == nil {
		dir = filepath.Dir(dir)
	}
}

func MoveSecret(dir, from, to, hash string, ids []age.Identity) ([]string, error) {
	if err := requireHash(hash); err != nil {
		return nil, err
	}
	src, err := SecretPath(dir, from)
	if err != nil {
		return nil, err
	}
	dst, err := SecretPath(dir, to)
	if err != nil {
		return nil, err
	}
	if src == dst {
		return nil, nil
	}
	unlock := fsx.Lock(src)
	defer unlock()
	b, err := os.ReadFile(src)
	if err != nil {
		return nil, err
	}
	if hash != Fingerprint(b) {
		return nil, ErrStale
	}
	rule, err := sops.RuleFor(dir, dst)
	if err != nil {
		return nil, err
	}
	if have, err := sops.Recipients(b); err != nil || !sops.SameRecipients(have, rule.Age) {
		if b, err = sops.Rekey(b, ids, rule.Age); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return nil, err
	}
	if err := create(dst, 0o644, b); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("%s already exists", to)
		}
		return nil, err
	}
	if err := os.Remove(src); err != nil {
		return nil, err
	}
	pruneEmpty(dir, filepath.Dir(src))
	var touched []string
	for _, f := range []func() (string, error){
		func() (string, error) { return UnlistResource(dir, from) },
		func() (string, error) { return ListResource(dir, to) },
	} {
		k, err := f()
		if err != nil {
			return touched, err
		}
		if k != "" {
			touched = append(touched, k)
		}
	}
	return touched, nil
}
