package cluster

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var talosconfigs = sync.OnceValues(func() ([2][]byte, error) {
	c, err := config.Parse([]byte(threeControlPlanes))
	if err != nil {
		return [2][]byte{}, err
	}
	var out [2][]byte
	for i := range out {
		gen, err := config.Generate(c, nil, config.FixedInstaller("installer"))
		if err != nil {
			return out, err
		}
		if out[i], err = gen.Talosconfig.Bytes(); err != nil {
			return out, err
		}
	}
	return out, nil
})

func TestTalosClientIsCachedPerNodeAndTalosconfig(t *testing.T) {
	tcs, err := talosconfigs()
	if err != nil {
		t.Fatal(err)
	}
	st := testStore(t)
	ctx := context.Background()
	m := NewManager(st, t.TempDir())
	first, err := m.talosClientFor("lab", "10.0.0.1", tcs[0])
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := m.talosClientFor("lab", "10.0.0.1", tcs[0]); again != first {
		t.Error("an unchanged talosconfig must reuse the client")
	}
	if other, _ := m.talosClientFor("lab", "10.0.0.2", tcs[0]); other == first {
		t.Error("each node has its own client")
	}
	rotated, err := m.talosClientFor("lab", "10.0.0.1", tcs[1])
	if err != nil {
		t.Fatal(err)
	}
	if rotated == first {
		t.Error("a new talosconfig must replace the client")
	}
	m.noteTalosErr("lab", "10.0.0.1", rotated, status.Error(codes.PermissionDenied, "nope"))
	if again, _ := m.talosClientFor("lab", "10.0.0.1", tcs[1]); again != rotated {
		t.Error("an answered call keeps the client")
	}
	m.noteTalosErr("lab", "10.0.0.1", rotated, status.Error(codes.DeadlineExceeded, "slow"))
	if again, _ := m.talosClientFor("lab", "10.0.0.1", tcs[1]); again != rotated {
		t.Error("a slow answer on a healthy connection keeps the client")
	}
	m.noteTalosErr("lab", "10.0.0.1", rotated, status.Error(codes.Unavailable, "connection refused"))
	if again, _ := m.talosClientFor("lab", "10.0.0.1", tcs[1]); again == rotated {
		t.Error("an unavailable node must get a fresh client")
	}
	m.keepTalos("lab", map[string]bool{"10.0.0.1": true})
	m.talosMu.Lock()
	_, kept := m.talos["lab"]["10.0.0.2"]
	m.talosMu.Unlock()
	if kept {
		t.Error("a node no longer declared must drop its client")
	}
	if err := st.PutCluster(ctx, store.ClusterRow{Name: "lab", Spec: []byte("x"), State: StateReady}); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteCluster(ctx, "lab"); err != nil {
		t.Fatal(err)
	}
	m.talosMu.Lock()
	_, kept = m.talos["lab"]
	m.talosMu.Unlock()
	if kept {
		t.Error("forgetting the cluster must drop its clients")
	}
	if _, err := m.talosClientFor("lab", "10.0.0.1", tcs[0]); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a deleted cluster must not get new clients: %v", err)
	}
	m.talosMu.Lock()
	_, kept = m.talos["lab"]
	m.talosMu.Unlock()
	if kept {
		t.Error("a deleted cluster must not be cached again")
	}
	if err := st.PutCluster(ctx, store.ClusterRow{Name: "lab", Spec: []byte("x"), State: StateReady}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.talosClientFor("lab", "10.0.0.1", tcs[0]); err != nil {
		t.Errorf("a recreated cluster gets clients again: %v", err)
	}
}

func TestStatusOfAMissingClusterDropsItsClients(t *testing.T) {
	tcs, err := talosconfigs()
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(testStore(t), t.TempDir())
	if _, err := m.talosClientFor("gone", "10.0.0.1", tcs[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Status(context.Background(), "gone"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("status of a missing cluster: %v", err)
	}
	m.talosMu.Lock()
	_, kept := m.talos["gone"]
	m.talosMu.Unlock()
	if kept {
		t.Error("a missing cluster must drop its clients")
	}
}
