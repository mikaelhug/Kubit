package store

import (
	"bytes"
	"testing"
)

func TestNextLabHostIndexNeverReusesALiveIndex(t *testing.T) {
	c, _ := NewCrypto(bytes.Repeat([]byte{4}, 32))
	s, err := Open(t.TempDir(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := t.Context()
	if n := s.NextLabHostIndex(ctx); n != 1 {
		t.Fatalf("first index %d", n)
	}
	for i, mac := range []string{"aa:00:00:00:00:01", "aa:00:00:00:00:02"} {
		if err := s.UpsertNode(ctx, Machine{MAC: mac, State: "labhost"}); err != nil {
			t.Fatal(err)
		}
		if err := s.SetLabHost(ctx, mac, &LabHost{State: "ready", Index: s.NextLabHostIndex(ctx)}); err != nil {
			t.Fatal(err)
		}
		if m, _ := s.GetMachine(ctx, mac); m.LabHost.Index != i+1 {
			t.Fatalf("%s got index %d", mac, m.LabHost.Index)
		}
	}
	if err := s.SetLabHost(ctx, "aa:00:00:00:00:01", nil); err != nil {
		t.Fatal(err)
	}
	if n := s.NextLabHostIndex(ctx); n != 3 {
		t.Errorf("after releasing host 1 the next index is %d; 2 is still in use", n)
	}
}
