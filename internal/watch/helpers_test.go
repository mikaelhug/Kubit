package watch

import (
	"testing"

	"github.com/mikaelhug/kubit/internal/cluster"
	"github.com/mikaelhug/kubit/internal/store"
)

func testWatcher(t *testing.T) *Watcher {
	t.Helper()
	return New(cluster.NewManager(store.New(), t.TempDir()), 0)
}
