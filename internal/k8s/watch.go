package k8s

import (
	"context"
	"sync"
	"time"

	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
)

const (
	ScopeWorkloads = "workloads"
	ScopeNetwork   = "network"
	ScopeStorage   = "storage"
	ScopeNodes     = "nodes"
	ScopeFlux      = "flux"
)

func (c *Client) WatchScopes(ctx context.Context, changed func(scope string)) {
	cs, err := c.streamClient()
	if err != nil {
		return
	}
	f := informers.NewSharedInformerFactory(cs, 0)
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
	_, _ = f.Batch().V1().CronJobs().Informer().AddEventHandler(hook(ScopeWorkloads))
	_, _ = f.Core().V1().Namespaces().Informer().AddEventHandler(hook(ScopeWorkloads))
	_, _ = f.Core().V1().Services().Informer().AddEventHandler(hook(ScopeNetwork))
	_, _ = f.Discovery().V1().EndpointSlices().Informer().AddEventHandler(hook(ScopeNetwork))
	_, _ = f.Networking().V1().Ingresses().Informer().AddEventHandler(hook(ScopeNetwork))
	_, _ = f.Core().V1().PersistentVolumeClaims().Informer().AddEventHandler(hook(ScopeStorage))
	_, _ = f.Core().V1().PersistentVolumes().Informer().AddEventHandler(hook(ScopeStorage))
	_, _ = f.Storage().V1().StorageClasses().Informer().AddEventHandler(hook(ScopeStorage))
	_, _ = f.Core().V1().Nodes().Informer().AddEventHandler(hook(ScopeNodes))
	go c.watchFlux(ctx, hook(ScopeFlux))
	f.Start(ctx.Done())
	f.WaitForCacheSync(ctx.Done())
	<-ctx.Done()
	f.Shutdown()
}

type Debouncer struct {
	delay   time.Duration
	maxWait time.Duration
	fire    func(key string)
	mu      sync.Mutex
	pending map[string]*burst
	stopped bool
}

type burst struct {
	first time.Time
	timer *time.Timer
}

func NewDebouncer(delay, maxWait time.Duration, fire func(key string)) *Debouncer {
	return &Debouncer{delay: delay, maxWait: max(maxWait, delay), fire: fire, pending: map[string]*burst{}}
}

func (d *Debouncer) Hit(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return
	}
	now := time.Now()
	b := d.pending[key]
	if b == nil {
		b = &burst{first: now}
		d.pending[key] = b
		b.timer = time.AfterFunc(d.delay, func() { d.flush(key, b) })
		return
	}
	b.timer.Reset(max(min(d.delay, b.first.Add(d.maxWait).Sub(now)), 0))
}

func (d *Debouncer) flush(key string, b *burst) {
	d.mu.Lock()
	if d.stopped || d.pending[key] != b {
		d.mu.Unlock()
		return
	}
	delete(d.pending, key)
	d.mu.Unlock()
	d.fire(key)
}

func (d *Debouncer) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopped = true
	for key, b := range d.pending {
		b.timer.Stop()
		delete(d.pending, key)
	}
}
