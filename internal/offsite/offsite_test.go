package offsite

import (
	"context"
	"strings"
	"testing"
)

func TestDirStoreRoundTripAndPrune(t *testing.T) {
	st, err := Open(Target{Type: "dir", Dir: t.TempDir(), Prefix: "kubit"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := Probe(ctx, st); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"backups/20260101T000000Z.kubitbak", "backups/20260102T000000Z.kubitbak", "backups/20260103T000000Z.kubitbak", "lab/snapshots/a.gz.sealed"} {
		if err := st.Put(ctx, k, strings.NewReader("x"), 1); err != nil {
			t.Fatal(err)
		}
	}
	n, err := PruneOldest(ctx, st, "backups", 2)
	if err != nil || n != 1 {
		t.Fatalf("prune: n=%d err=%v", n, err)
	}
	objs, _ := st.List(ctx, "backups")
	if len(objs) != 2 || objs[0].Key == "backups/20260101T000000Z.kubitbak" || objs[1].Key == "backups/20260101T000000Z.kubitbak" {
		t.Fatalf("oldest should be gone: %+v", objs)
	}
	if objs, _ := st.List(ctx, "missing"); len(objs) != 0 {
		t.Fatalf("missing prefix should list empty, got %+v", objs)
	}
	if err := st.Delete(ctx, "nope"); err != nil {
		t.Fatalf("delete of missing key should be nil, got %v", err)
	}
}
