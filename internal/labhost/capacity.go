package labhost

import (
	"context"
	"sync"
	"time"
)

const CapacityTTL = time.Hour

type freshCapacity struct{}

func FreshCapacity(ctx context.Context) context.Context {
	return context.WithValue(ctx, freshCapacity{}, true)
}

type capacityEntry struct {
	c  Capacity
	at time.Time
}

var capacities = struct {
	sync.Mutex
	m map[string]capacityEntry
}{m: map[string]capacityEntry{}}

func CachedCapacity(ctx context.Context, key string, read func(context.Context) (Capacity, error)) (Capacity, error) {
	if fresh, _ := ctx.Value(freshCapacity{}).(bool); !fresh {
		capacities.Lock()
		e, ok := capacities.m[key]
		capacities.Unlock()
		if ok && time.Since(e.at) < CapacityTTL {
			return e.c, nil
		}
	}
	c, err := read(ctx)
	capacities.Lock()
	defer capacities.Unlock()
	if err == nil && c.Ready {
		capacities.m[key] = capacityEntry{c, time.Now()}
	} else {
		delete(capacities.m, key)
	}
	return c, err
}

func ForgetCapacity(key string) {
	capacities.Lock()
	defer capacities.Unlock()
	delete(capacities.m, key)
}

type Observation struct {
	VMs        []VM
	ListErr    error
	Metrics    Metrics
	MetricsErr error
}

func Observe(ctx context.Context, d Driver) Observation {
	var o Observation
	var wg sync.WaitGroup
	wg.Go(func() { o.VMs, o.ListErr = d.List(ctx) })
	o.Metrics, o.MetricsErr = d.Metrics(ctx)
	wg.Wait()
	return o
}
