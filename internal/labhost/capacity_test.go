package labhost

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestCachedCapacityKeepsReadyHostsForAnHour(t *testing.T) {
	var reads atomic.Int32
	ready := true
	read := func(context.Context) (Capacity, error) {
		reads.Add(1)
		return Capacity{CPUs: int(reads.Load()), Ready: ready}, nil
	}
	ctx := context.Background()
	key := t.Name()
	defer ForgetCapacity(key)
	a, _ := CachedCapacity(ctx, key, read)
	b, _ := CachedCapacity(ctx, key, read)
	if reads.Load() != 1 || a.CPUs != b.CPUs {
		t.Fatalf("a ready host must be read once per TTL, read %d times", reads.Load())
	}
	if c, _ := CachedCapacity(FreshCapacity(ctx), key, read); c.CPUs != 2 {
		t.Errorf("FreshCapacity must read again, got %+v", c)
	}
	capacities.Lock()
	e := capacities.m[key]
	e.at = time.Now().Add(-CapacityTTL - time.Second)
	capacities.m[key] = e
	capacities.Unlock()
	if c, _ := CachedCapacity(ctx, key, read); c.CPUs != 3 {
		t.Errorf("an expired entry must be read again, got %+v", c)
	}
	ForgetCapacity(key)
	ready = false
	CachedCapacity(ctx, key, read)
	CachedCapacity(ctx, key, read)
	if reads.Load() != 5 {
		t.Errorf("a host that is not ready must be read every time, read %d times", reads.Load())
	}
	if _, err := CachedCapacity(ctx, key, func(context.Context) (Capacity, error) { return Capacity{Ready: true}, errors.New("boom") }); err == nil {
		t.Error("errors pass through")
	}
}

type slowDriver struct {
	Driver
	both chan struct{}
}

func (d slowDriver) List(ctx context.Context) ([]VM, error) {
	select {
	case d.both <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	<-d.both
	return []VM{{Name: "vm-01"}}, nil
}

func (d slowDriver) Metrics(ctx context.Context) (Metrics, error) {
	select {
	case <-d.both:
		d.both <- struct{}{}
		return Metrics{VMsRunning: 1}, nil
	case <-ctx.Done():
		return Metrics{}, ctx.Err()
	}
}

func TestObserveListsAndMeasuresConcurrently(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	o := Observe(ctx, slowDriver{both: make(chan struct{})})
	if o.ListErr != nil || o.MetricsErr != nil || len(o.VMs) != 1 || o.Metrics.VMsRunning != 1 {
		t.Errorf("observation: %+v", o)
	}
}
