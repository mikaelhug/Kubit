package store_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/mikael/kubit/internal/store"
)

func TestSOPSKeySealedAndOutlivesCluster(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if _, err := s.GetSOPSKey(ctx, "lab"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("no key yet: %v", err)
	}
	if err := s.PutCluster(ctx, store.ClusterRow{Name: "lab", Spec: []byte("spec: 1")}); err != nil {
		t.Fatal(err)
	}
	identity := []byte("AGE-SECRET-KEY-1TEST")
	if err := s.PutSOPSKey(ctx, "lab", identity, "age1test"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteCluster(ctx, "lab"); err != nil {
		t.Fatal(err)
	}
	k, err := s.GetSOPSKey(ctx, "lab")
	if err != nil || !bytes.Equal(k.Identity, identity) || k.Recipient != "age1test" || k.CreatedAt == "" {
		t.Fatalf("key after cluster delete: %+v %v", k, err)
	}
	if err := s.PutSOPSKey(ctx, "lab", []byte("AGE-SECRET-KEY-1NEW"), "age1new"); err != nil {
		t.Fatal(err)
	}
	if k, _ := s.GetSOPSKey(ctx, "lab"); k.Recipient != "age1new" {
		t.Fatalf("replace: %+v", k)
	}
	if err := s.DeleteSOPSKey(ctx, "lab"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSOPSKey(ctx, "lab"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted: %v", err)
	}
}

func TestCreateSOPSKeyKeepsTheFirst(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if err := s.CreateSOPSKey(ctx, "lab", []byte("AGE-SECRET-KEY-1FIRST"), "age1first"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSOPSKey(ctx, "lab", []byte("AGE-SECRET-KEY-1SECOND"), "age1second"); err != nil {
		t.Fatal(err)
	}
	if k, _ := s.GetSOPSKey(ctx, "lab"); k.Recipient != "age1first" {
		t.Errorf("a racing second create replaced the key: %+v", k)
	}
}
