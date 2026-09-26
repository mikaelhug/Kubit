package api

import (
	"sync"
	"time"
)

type throttle struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func (t *throttle) every(key string, d time.Duration, fn func()) {
	t.mu.Lock()
	if t.last == nil {
		t.last = map[string]time.Time{}
	}
	if time.Since(t.last[key]) < d {
		t.mu.Unlock()
		return
	}
	t.last[key] = time.Now()
	t.mu.Unlock()
	fn()
}
