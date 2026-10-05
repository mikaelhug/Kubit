package watch

import (
	"bytes"
	"testing"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/store"
)

func testWatcher(t *testing.T) *Watcher {
	t.Helper()
	c, _ := store.NewCrypto(bytes.Repeat([]byte{3}, 32))
	dir := t.TempDir()
	s, err := store.Open(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return New(cluster.NewManager(s, dir), 0)
}
