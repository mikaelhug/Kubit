package store

import (
	"context"
	"slices"
	"sync"
	"time"
)

type held struct {
	mu      sync.Mutex
	secrets map[string]ClusterSecrets
	sops    map[string]SOPSKey
}

func cloneSecrets(sec ClusterSecrets) ClusterSecrets {
	return ClusterSecrets{SecretsBundle: slices.Clone(sec.SecretsBundle), Talosconfig: slices.Clone(sec.Talosconfig), Kubeconfig: slices.Clone(sec.Kubeconfig)}
}

func (s *Store) HoldClusterSecrets(name string, sec ClusterSecrets) {
	s.held.mu.Lock()
	if s.held.secrets == nil {
		s.held.secrets = map[string]ClusterSecrets{}
	}
	s.held.secrets[name] = cloneSecrets(sec)
	s.held.mu.Unlock()
	s.notify(Change{Table: "secrets", Cluster: name, Key: name, Op: "put"})
}

func (s *Store) HoldSOPSKey(name string, identity []byte, recipient string) {
	s.held.mu.Lock()
	if s.held.sops == nil {
		s.held.sops = map[string]SOPSKey{}
	}
	s.held.sops[name] = SOPSKey{Cluster: name, Identity: slices.Clone(identity), Recipient: recipient, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	s.held.mu.Unlock()
	s.notify(Change{Table: "sops", Cluster: name, Key: name, Op: "put"})
}


func (s *Store) heldSecrets(name string, update func(*ClusterSecrets)) (*ClusterSecrets, bool) {
	s.held.mu.Lock()
	defer s.held.mu.Unlock()
	sec, ok := s.held.secrets[name]
	if !ok {
		return nil, false
	}
	if update != nil {
		update(&sec)
		s.held.secrets[name] = sec
	}
	out := cloneSecrets(sec)
	return &out, true
}

func (s *Store) heldSOPS(name string, update func(*SOPSKey)) (*SOPSKey, bool) {
	s.held.mu.Lock()
	defer s.held.mu.Unlock()
	k, ok := s.held.sops[name]
	if !ok {
		return nil, false
	}
	if update != nil {
		update(&k)
		s.held.sops[name] = k
	}
	out := k
	out.Identity = slices.Clone(k.Identity)
	return &out, true
}

func (s *Store) DropStoredSecrets(ctx context.Context, name, keepRecipient string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM cluster_secrets WHERE cluster = ?`, name); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM sops_keys WHERE cluster = ? AND recipient = ?`, name, keepRecipient)
	return err
}
