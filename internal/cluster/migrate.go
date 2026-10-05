package cluster

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"filippo.io/age"
	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/repo"
	"github.com/mikael/kubit/internal/tofu"
)

func (m *Manager) ExportRepo(ctx context.Context, name, dir string, recipients []string) (*repo.Repo, bool, error) {
	c, _, err := m.LoadCluster(ctx, name)
	if err != nil {
		return nil, false, err
	}
	sec, err := m.Store.GetClusterSecrets(ctx, name)
	if err != nil {
		return nil, false, err
	}
	bundle, err := config.ParseSecrets(sec.SecretsBundle)
	if err != nil {
		return nil, false, err
	}
	s := &repo.Secrets{Bundle: bundle, BundleYAML: sec.SecretsBundle}
	if s.StatePassphrase, err = repo.NewPassphrase(); err != nil {
		return nil, false, err
	}
	if s.FluxKey, err = m.fluxIdentity(ctx, name); err != nil {
		return nil, false, err
	}
	decl := c.Clone()
	decl.Spec.SchematicID = ""
	for i := range decl.Spec.Pools {
		decl.Spec.Pools[i].SchematicID = ""
	}
	r, err := repo.Write(dir, decl, s, recipients)
	if err != nil {
		return nil, false, err
	}
	moved, err := m.moveState(ctx, name, r.StatePath(), s.StatePassphrase)
	if err != nil {
		return r, false, fmt.Errorf("repo written, platform state not moved: %w", err)
	}
	return r, moved, nil
}

func (m *Manager) fluxIdentity(ctx context.Context, name string) (string, error) {
	k, err := m.Store.GetSOPSKey(ctx, name)
	if err != nil {
		id, err := age.GenerateX25519Identity()
		if err != nil {
			return "", err
		}
		return id.String(), nil
	}
	ids, err := age.ParseIdentities(bytes.NewReader(k.Identity))
	if err != nil || len(ids) != 1 {
		return "", fmt.Errorf("stored flux key: %v", err)
	}
	id, ok := ids[0].(*age.X25519Identity)
	if !ok {
		return "", errors.New("stored flux key is not an X25519 age key")
	}
	return id.String(), nil
}

func (m *Manager) moveState(ctx context.Context, name, target, passphrase string) (bool, error) {
	old := filepath.Join(m.platformDir(name), "terraform.tfstate")
	if _, err := os.Stat(old); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if _, err := os.Stat(target); err == nil {
		return false, fmt.Errorf("%s already exists", target)
	}
	bin, err := m.tofuBin(ctx)
	if err != nil {
		return false, err
	}
	aside := old + ".moved"
	if err := os.Rename(old, aside); err != nil {
		return false, err
	}
	r := &tofu.Runner{Bin: bin, Dir: m.platformDir(name), StatePath: target, Passphrase: passphrase}
	err = r.Init(ctx)
	if err == nil {
		err = r.PushState(ctx, aside)
	}
	if err != nil {
		_ = os.Rename(aside, old)
		return false, err
	}
	b, err := os.ReadFile(target)
	if err != nil {
		return false, err
	}
	if !bytes.Contains(b, []byte(`"encrypted_data"`)) {
		return false, fmt.Errorf("%s was written unencrypted", target)
	}
	return true, nil
}
