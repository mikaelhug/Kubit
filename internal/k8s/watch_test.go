package k8s

import (
	"sync"
	"testing"
	"time"
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

func TestDebouncerCoalescesABurst(t *testing.T) {
	f := newFires()
	d := NewDebouncer(50*time.Millisecond, time.Second, f.record)
	defer d.Stop()
	for range 20 {
		d.Hit("workloads")
	}
	d.Hit("network")
	time.Sleep(200 * time.Millisecond)
	if f.count("workloads") != 1 || f.count("network") != 1 {
		t.Fatalf("fires: %v", f.at)
	}
	time.Sleep(100 * time.Millisecond)
	if f.count("workloads") != 1 {
		t.Errorf("a burst fired twice: %v", f.at)
	}
}

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

func TestDebouncerStopPreventsFiring(t *testing.T) {
	f := newFires()
	d := NewDebouncer(30*time.Millisecond, 100*time.Millisecond, f.record)
	d.Hit("workloads")
	d.Stop()
	d.Hit("nodes")
	time.Sleep(100 * time.Millisecond)
	if f.count("workloads")+f.count("nodes") != 0 {
		t.Errorf("fired after Stop: %v", f.at)
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
	if c.rest.Timeout != 15*time.Second {
		t.Errorf("requests keep their timeout, got %s", c.rest.Timeout)
	}
	if got := c.streamConfig().Timeout; got != 0 {
		t.Errorf("watches and followed logs must not time out, got %s", got)
	}
	if c.rest.Timeout != 15*time.Second {
		t.Error("streamConfig must not change the request config")
	}
}
