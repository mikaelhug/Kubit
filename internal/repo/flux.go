package repo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"filippo.io/age"
	"github.com/mikael/kubit/internal/gitremote"
	"github.com/mikael/kubit/internal/sops"
	"go.yaml.in/yaml/v4"
)

const sampleSecret = "apps/example.sops.yaml"

func FluxReads(dir, root, recipient string) bool {
	r, err := sops.RuleFor(dir, filepath.Join(dir, filepath.FromSlash(root), sampleSecret))
	return err == nil && slices.Contains(r.Age, recipient)
}

func LetFluxDecrypt(dir, recipient string, ids []age.Identity) (int, error) {
	if _, err := sops.AddRecipient(dir, recipient, SecretsFile); err != nil {
		return 0, err
	}
	files, err := SecretFiles(dir)
	if err != nil {
		return 0, err
	}
	n := 0
	var errs []error
	for _, f := range files {
		if f.Error != "" || slices.Contains(f.Recipients, recipient) {
			continue
		}
		path := filepath.Join(dir, filepath.FromSlash(f.Path))
		rule, err := sops.RuleFor(dir, path)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", f.Path, err))
			continue
		}
		err = replace(path, f.Hash, 0o644, func(b []byte) ([]byte, error) { return sops.Rekey(b, ids, rule.Age) })
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", f.Path, err))
			continue
		}
		n++
	}
	return n, errors.Join(errs...)
}

var ErrNotSSH = errors.New("platform.flux.repository.url is not an ssh:// URL")

type DeployKey struct {
	*gitremote.Key
	Hosts []gitremote.Host `json:"hosts"`
	Hash  string           `json:"hash"`
}

func DeployKeyOf(dir string, ids []age.Identity) (DeployKey, error) {
	enc, err := os.ReadFile(filepath.Join(dir, SecretsFile))
	if err != nil {
		return DeployKey{}, err
	}
	plain, err := sops.Decrypt(enc, ids)
	if err != nil {
		return DeployKey{}, fmt.Errorf("%s: %w", SecretsFile, err)
	}
	s, err := parseSecrets(plain)
	if err != nil {
		return DeployKey{}, fmt.Errorf("%s: %w", SecretsFile, err)
	}
	k := DeployKey{Hosts: gitremote.Hosts(s.KnownHosts), Hash: Fingerprint(enc)}
	if s.DeployKey != "" {
		pub, err := gitremote.PublicKey(s.DeployKey)
		if err != nil {
			return DeployKey{}, err
		}
		k.Key = &pub
	}
	return k, nil
}

func NewDeployKey(ctx context.Context, dir, hash string, hostsOnly bool, ids []age.Identity) error {
	if err := requireHash(hash); err != nil {
		return err
	}
	c, _, err := LoadSpec(dir)
	if err != nil {
		return err
	}
	r := c.Spec.Platform.Flux.Repository
	if r == nil || !gitremote.IsSSH(r.URL) {
		return ErrNotSSH
	}
	hosts, err := gitremote.ScanHosts(ctx, r.URL)
	if err != nil {
		return err
	}
	key := ""
	if !hostsOnly {
		if key, err = gitremote.NewKey("kubit-" + c.Metadata.Name); err != nil {
			return err
		}
	}
	return replace(filepath.Join(dir, SecretsFile), hash, 0o644, func(b []byte) ([]byte, error) {
		return sops.Edit(b, ids, func(root *yaml.Node) error {
			if key != "" {
				if err := sops.Set(root, []string{"flux", "deployKey"}, key); err != nil {
					return err
				}
			}
			return sops.Set(root, []string{"flux", "knownHosts"}, hosts)
		})
	})
}
