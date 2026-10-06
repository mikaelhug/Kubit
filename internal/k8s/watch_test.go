package k8s

import (
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
)

type fires struct {
	mu sync.Mutex
	at map[string][]time.Time
}

func (f *fires) record(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.at[key] = append(f.at[key], time.Now())
}

func (f *fires) count(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.at[key])
}

func newFires() *fires { return &fires{at: map[string][]time.Time{}} }

func TestDebouncerFiresWithinMaxWaitUnderChurn(t *testing.T) {
	f := newFires()
	d := NewDebouncer(50*time.Millisecond, 150*time.Millisecond, f.record)
	defer d.Stop()
	start := time.Now()
	for time.Since(start) < 500*time.Millisecond {
		d.Hit("workloads")
		time.Sleep(10 * time.Millisecond)
	}
	f.mu.Lock()
	got := append([]time.Time(nil), f.at["workloads"]...)
	f.mu.Unlock()
	if len(got) < 2 {
		t.Fatalf("continuous churn must still fire, got %d", len(got))
	}
	if first := got[0].Sub(start); first > 300*time.Millisecond {
		t.Errorf("first fire after %s, want within max wait", first)
	}
	time.Sleep(150 * time.Millisecond)
	if n := f.count("workloads"); n > len(got)+1 {
		t.Errorf("trailing fires: %d after churn ended at %d", n, len(got))
	}
}

func TestStreamConfigHasNoTimeout(t *testing.T) {
	kubeconfig := []byte(`apiVersion: v1
kind: Config
clusters:
- name: c
  cluster:
    server: https://127.0.0.1:6443
contexts:
- name: c
  context:
    cluster: c
    user: u
current-context: c
users:
- name: u
  user:
    token: t
`)
	c, err := New(kubeconfig)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.streamConfig().Timeout; got != 0 {
		t.Errorf("watches and followed logs must not time out, got %s", got)
	}
}

func TestObjectNamespaceUnwrapsInformerTombstones(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "metallb-system"}}
	if got := objectNamespace(cache.DeletedFinalStateUnknown{Key: "metallb-system/p", Obj: pod}); got != "metallb-system" {
		t.Errorf("objectNamespace of a tombstone = %q", got)
	}
}
