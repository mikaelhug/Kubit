package repo

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"

	"filippo.io/age"
	"github.com/mikael/kubit/internal/sops"
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
