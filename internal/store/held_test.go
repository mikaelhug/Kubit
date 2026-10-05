package store_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/mikael/kubit/internal/store"
)

func TestHeldSecretsShadowAndReplaceTheStoredOnes(t *testing.T) {
	crypto, _ := store.NewCrypto(bytes.Repeat([]byte{9}, 32))
	dir := t.TempDir()
	s, err := store.Open(dir, crypto)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.PutCluster(ctx, store.ClusterRow{Name: "lab", Spec: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutClusterSecrets(ctx, "lab", store.ClusterSecrets{SecretsBundle: []byte("db"), Talosconfig: []byte("t"), Kubeconfig: []byte("k")}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutSOPSKey(ctx, "lab", []byte("id"), "age1old"); err != nil {
		t.Fatal(err)
	}
	s.HoldClusterSecrets("lab", store.ClusterSecrets{SecretsBundle: []byte("repo"), Talosconfig: []byte("t2")})
	s.HoldSOPSKey("lab", []byte("id2"), "age1new")
	if err := s.SetKubeconfig(ctx, "lab", []byte("k2")); err != nil {
		t.Fatal(err)
	}
	sec, err := s.GetClusterSecrets(ctx, "lab")
	if err != nil || string(sec.SecretsBundle) != "repo" || string(sec.Kubeconfig) != "k2" {
		t.Fatalf("held secrets %+v %v", sec, err)
	}
	if k, _ := s.GetSOPSKey(ctx, "lab"); k.Recipient != "age1new" {
		t.Errorf("held sops key %s", k.Recipient)
	}
	if err := s.DropStoredSecrets(ctx, "lab", "age1new"); err != nil {
		t.Fatal(err)
	}
	fresh, err := store.Open(dir, crypto)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if _, err := fresh.GetClusterSecrets(ctx, "lab"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("stored secrets must be gone: %v", err)
	}
	if k, err := fresh.GetSOPSKey(ctx, "lab"); err != nil || k.Recipient != "age1old" {
		t.Errorf("a stored flux key the repo does not hold must stay: %+v %v", k, err)
	}
}
