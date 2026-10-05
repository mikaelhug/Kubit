package repo

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/sops"
	talosconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"go.yaml.in/yaml/v4"
)

const (
	ClusterFile = "cluster.yaml"
	SecretsFile = "secrets.sops.yaml"
	StateDir    = "state"
)

var ignored = []string{"talosconfig", "kubeconfig", "*.plain.yaml", ".terraform/"}

type Repo struct {
	Dir     string
	Spec    []byte
	Cluster *config.Cluster
	Secrets *Secrets
}

type Secrets struct {
	Bundle          *secrets.Bundle
	BundleYAML      []byte
	FluxKey         string
	StatePassphrase string
	BackupKeyID     string
	BackupSecret    string
}

type secretsDoc struct {
	Talos    yaml.Node `yaml:"talos"`
	Flux     fluxDoc   `yaml:"flux"`
	Platform stateDoc  `yaml:"platform"`
	Backup   backupDoc `yaml:"backup,omitempty"`
}

type backupDoc struct {
	AccessKeyID     string `yaml:"accessKeyID,omitempty"`
	SecretAccessKey string `yaml:"secretAccessKey,omitempty"`
}

type fluxDoc struct {
	AgeKey string `yaml:"ageKey"`
}

type stateDoc struct {
	StatePassphrase string `yaml:"statePassphrase"`
}

func LoadSpec(dir string) (*config.Cluster, []byte, error) {
	b, err := os.ReadFile(filepath.Join(dir, ClusterFile))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, fmt.Errorf("%s: no %s; run kubit init", dir, ClusterFile)
		}
		return nil, nil, err
	}
	c, err := config.Parse(b)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", filepath.Join(dir, ClusterFile), err)
	}
	return c, b, nil
}

func Load(dir string) (*Repo, error) {
	ids, err := sops.Identities()
	if err != nil {
		return nil, err
	}
	return LoadWith(dir, ids)
}

func LoadWith(dir string, ids []age.Identity) (*Repo, error) {
	c, spec, err := LoadSpec(dir)
	if err != nil {
		return nil, err
	}
	enc, err := os.ReadFile(filepath.Join(dir, SecretsFile))
	if err != nil {
		return nil, err
	}
	plain, err := sops.Decrypt(enc, ids)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", SecretsFile, err)
	}
	s, err := parseSecrets(plain)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", SecretsFile, err)
	}
	return &Repo{Dir: dir, Spec: spec, Cluster: c, Secrets: s}, nil
}

func parseSecrets(plain []byte) (*Secrets, error) {
	var doc secretsDoc
	if err := yaml.Unmarshal(plain, &doc); err != nil {
		return nil, err
	}
	if doc.Talos.Kind == 0 {
		return nil, errors.New("no talos secrets bundle")
	}
	raw, err := yaml.Marshal(&doc.Talos)
	if err != nil {
		return nil, err
	}
	b, err := config.ParseSecrets(raw)
	if err != nil {
		return nil, err
	}
	if b.Cluster == nil || b.Certs == nil || b.Certs.OS == nil || b.Certs.K8s == nil {
		return nil, errors.New("incomplete talos secrets bundle")
	}
	return &Secrets{Bundle: b, BundleYAML: raw, FluxKey: doc.Flux.AgeKey, StatePassphrase: doc.Platform.StatePassphrase, BackupKeyID: doc.Backup.AccessKeyID, BackupSecret: doc.Backup.SecretAccessKey}, nil
}

func NewPassphrase() (string, error) {
	pass := make([]byte, 32)
	if _, err := rand.Read(pass); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(pass), nil
}

func NewSecrets(talosVersion string) (*Secrets, error) {
	contract, err := talosconfig.ParseContractFromVersion(talosVersion)
	if err != nil {
		return nil, fmt.Errorf("talosVersion: %w", err)
	}
	b, err := secrets.NewBundle(secrets.NewClock(), contract)
	if err != nil {
		return nil, err
	}
	raw, err := yaml.Marshal(b)
	if err != nil {
		return nil, err
	}
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, err
	}
	pass, err := NewPassphrase()
	if err != nil {
		return nil, err
	}
	return &Secrets{Bundle: b, BundleYAML: raw, FluxKey: id.String(), StatePassphrase: pass}, nil
}

func (s *Secrets) plain() ([]byte, error) {
	var doc secretsDoc
	if err := yaml.Unmarshal(s.BundleYAML, &doc.Talos); err != nil {
		return nil, err
	}
	if doc.Talos.Kind == yaml.DocumentNode && len(doc.Talos.Content) == 1 {
		doc.Talos = *doc.Talos.Content[0]
	}
	doc.Flux.AgeKey, doc.Platform.StatePassphrase = s.FluxKey, s.StatePassphrase
	doc.Backup = backupDoc{AccessKeyID: s.BackupKeyID, SecretAccessKey: s.BackupSecret}
	return yaml.Marshal(&doc)
}

func (s *Secrets) FluxRecipient() string {
	id, err := age.ParseX25519Identity(s.FluxKey)
	if err != nil {
		return ""
	}
	return id.Recipient().String()
}

func Init(dir string, c *config.Cluster, recipients []string) (*Repo, error) {
	s, err := NewSecrets(c.Spec.TalosVersion)
	if err != nil {
		return nil, err
	}
	return Write(dir, c, s, recipients)
}

func Write(dir string, c *config.Cluster, s *Secrets, recipients []string) (*Repo, error) {
	for _, f := range []string{ClusterFile, SecretsFile} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return nil, fmt.Errorf("%s already exists; kubit init never overwrites it", filepath.Join(dir, f))
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	rule, err := sopsRule(dir, recipients)
	if err != nil {
		return nil, err
	}
	if err := writeSecrets(dir, s, rule); err != nil {
		return nil, err
	}
	spec, err := c.Marshal()
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, ClusterFile), spec, 0o644); err != nil {
		return nil, err
	}
	if err := ensureIgnored(dir); err != nil {
		return nil, err
	}
	return &Repo{Dir: dir, Spec: spec, Cluster: c, Secrets: s}, nil
}

func sopsRule(dir string, recipients []string) (sops.Rule, error) {
	if _, err := os.Stat(filepath.Join(dir, sops.ConfigFile)); err == nil {
		return sops.RuleFor(dir, filepath.Join(dir, SecretsFile))
	}
	if len(recipients) == 0 {
		return sops.Rule{}, errors.New("no age recipient; pass --age or create " + sops.DefaultKeyFile())
	}
	if err := sops.WriteConfig(dir, recipients); err != nil {
		return sops.Rule{}, err
	}
	return sops.Rule{Age: recipients}, nil
}

func writeSecrets(dir string, s *Secrets, rule sops.Rule) error {
	plain, err := s.plain()
	if err != nil {
		return err
	}
	enc, err := sops.Encrypt(plain, rule)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, SecretsFile), enc, 0o644)
}

func ensureIgnored(dir string) error {
	p := filepath.Join(dir, ".gitignore")
	b, err := os.ReadFile(p)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	have := strings.Split(string(b), "\n")
	var add []string
	for _, pat := range ignored {
		if !slices.Contains(have, pat) {
			add = append(add, pat)
		}
	}
	if len(add) == 0 {
		return nil
	}
	if len(b) > 0 && !bytes.HasSuffix(b, []byte("\n")) {
		b = append(b, '\n')
	}
	b = append(b, strings.Join(add, "\n")+"\n"...)
	return os.WriteFile(p, b, 0o644)
}

func (r *Repo) StatePath() string { return filepath.Join(r.Dir, StateDir, "platform.tfstate") }

func Stamp() string { return time.Now().UTC().Format(time.RFC3339) }
