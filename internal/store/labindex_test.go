package store

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
)

func openLab(t *testing.T) *Store {
	t.Helper()
	c, _ := NewCrypto(bytes.Repeat([]byte{4}, 32))
	s, err := Open(t.TempDir(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestLabHostIndexNeverReusesALiveIndex(t *testing.T) {
	s := openLab(t)
	ctx := t.Context()
	for i, mac := range []string{"aa:00:00:00:00:01", "aa:00:00:00:00:02"} {
		if err := s.UpsertNode(ctx, Machine{MAC: mac, State: "labhost"}); err != nil {
			t.Fatal(err)
		}
		lh := &LabHost{State: "ready"}
		if err := s.SetLabHost(ctx, mac, lh); err != nil {
			t.Fatal(err)
		}
		if m, _ := s.GetMachine(ctx, mac); m.LabHost.Index != i+1 || lh.Index != i+1 {
			t.Fatalf("%s got index %d (record %d)", mac, lh.Index, m.LabHost.Index)
		}
	}
	if err := s.UpdateLabHost(ctx, "aa:00:00:00:00:02", func(l *LabHost) { l.State = "updating" }); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.GetMachine(ctx, "aa:00:00:00:00:02"); m.LabHost.Index != 2 {
		t.Errorf("an update must keep the index, got %d", m.LabHost.Index)
	}
	if err := s.SetLabHost(ctx, "aa:00:00:00:00:01", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertNode(ctx, Machine{MAC: "aa:00:00:00:00:03", State: "labhost"}); err != nil {
		t.Fatal(err)
	}
	lh := &LabHost{State: "ready"}
	if err := s.SetLabHost(ctx, "aa:00:00:00:00:03", lh); err != nil {
		t.Fatal(err)
	}
	if lh.Index != 3 {
		t.Errorf("after releasing host 1 the next index is %d; 2 is still in use", lh.Index)
	}
}

func TestLabHostIndexUnderConcurrency(t *testing.T) {
	s := openLab(t)
	ctx := t.Context()
	const n = 16
	for i := range n {
		if err := s.UpsertNode(ctx, Machine{MAC: fmt.Sprintf("aa:00:00:00:01:%02x", i), State: "labhost"}); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	got := make([]int, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lh := &LabHost{State: "installing"}
			if err := s.SetLabHost(ctx, fmt.Sprintf("aa:00:00:00:01:%02x", i), lh); err != nil {
				t.Error(err)
			}
			got[i] = lh.Index
		}()
	}
	wg.Wait()
	seen := map[int]bool{}
	for _, idx := range got {
		if idx < 1 || idx > n || seen[idx] {
			t.Fatalf("indices must be distinct in 1..%d: %v", n, got)
		}
		seen[idx] = true
	}
}

func TestSSHKeyIsMintedOnce(t *testing.T) {
	s := openLab(t)
	ctx := t.Context()
	var wg sync.WaitGroup
	pubs := make([]string, 8)
	privs := make([]string, 8)
	for i := range pubs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			priv, pub, err := s.SSHKey(ctx)
			if err != nil {
				t.Error(err)
			}
			pubs[i], privs[i] = pub, string(priv)
		}()
	}
	wg.Wait()
	priv, pub, err := s.SSHKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := range pubs {
		if pubs[i] != pub || privs[i] != string(priv) {
			t.Fatalf("caller %d got a different key pair than the one stored", i)
		}
	}
}
