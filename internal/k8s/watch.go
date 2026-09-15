package k8s

import (
	"context"
	"time"

	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
)

// Scopes are the console views a change invalidates. The UI refetches a view only
// when its scope fires, so nothing is polled and nothing is fetched needlessly.
const (
	ScopeWorkloads = "workloads"
	ScopeNetwork   = "network"
	ScopeStorage   = "storage"
	ScopeNodes     = "nodes"
)

// WatchScopes runs shared informers for everything the console shows and calls
// changed(scope) on every add/update/delete until ctx ends. The initial list is not
// reported (the caller fetched it already). Returns when the informers stop.
func (c *Client) WatchScopes(ctx context.Context, changed func(scope string)) {
	f := informers.NewSharedInformerFactory(c.Clientset, 0)
	hook := func(scope string) cache.ResourceEventHandlerFuncs {
		return cache.ResourceEventHandlerFuncs{
			AddFunc:    func(any) { changed(scope) },
			UpdateFunc: func(_, _ any) { changed(scope) },
			DeleteFunc: func(any) { changed(scope) },
		}
	}
	_, _ = f.Core().V1().Pods().Informer().AddEventHandler(hook(ScopeWorkloads))
	_, _ = f.Apps().V1().Deployments().Informer().AddEventHandler(hook(ScopeWorkloads))
	_, _ = f.Apps().V1().DaemonSets().Informer().AddEventHandler(hook(ScopeWorkloads))
	_, _ = f.Apps().V1().StatefulSets().Informer().AddEventHandler(hook(ScopeWorkloads))
	_, _ = f.Batch().V1().Jobs().Informer().AddEventHandler(hook(ScopeWorkloads))
	_, _ = f.Core().V1().Services().Informer().AddEventHandler(hook(ScopeNetwork))
	_, _ = f.Discovery().V1().EndpointSlices().Informer().AddEventHandler(hook(ScopeNetwork))
	_, _ = f.Networking().V1().Ingresses().Informer().AddEventHandler(hook(ScopeNetwork))
	_, _ = f.Core().V1().PersistentVolumeClaims().Informer().AddEventHandler(hook(ScopeStorage))
	_, _ = f.Core().V1().PersistentVolumes().Informer().AddEventHandler(hook(ScopeStorage))
	_, _ = f.Storage().V1().StorageClasses().Informer().AddEventHandler(hook(ScopeStorage))
	_, _ = f.Core().V1().Nodes().Informer().AddEventHandler(hook(ScopeNodes))
	f.Start(ctx.Done())
	f.WaitForCacheSync(ctx.Done())
	<-ctx.Done()
	f.Shutdown()
}

// Debouncer coalesces bursts of changes per key into one callback.
type Debouncer struct {
	delay  time.Duration
	timers map[string]*time.Timer
	fire   func(key string)
	mu     chan struct{}
}

func NewDebouncer(delay time.Duration, fire func(key string)) *Debouncer {
	return &Debouncer{delay: delay, timers: map[string]*time.Timer{}, fire: fire, mu: make(chan struct{}, 1)}
}

func (d *Debouncer) Hit(key string) {
	d.mu <- struct{}{}
	defer func() { <-d.mu }()
	if t, ok := d.timers[key]; ok {
		t.Reset(d.delay)
		return
	}
	d.timers[key] = time.AfterFunc(d.delay, func() {
		d.mu <- struct{}{}
		delete(d.timers, key)
		<-d.mu
		d.fire(key)
	})
}
