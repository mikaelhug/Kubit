package k8s

import (
	"context"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/metadata/metadatainformer"
	"k8s.io/client-go/tools/cache"
)

const (
	ScopeWorkloads = "workloads"
	ScopeNetwork   = "network"
	ScopeStorage   = "storage"
	ScopeNodes     = "nodes"
	ScopeFlux      = "flux"
	ScopeAddons    = "addons"
)

func (c *Client) WatchScopes(ctx context.Context, changed func(scope, namespace string)) {
	cs, err := c.streamClient()
	if err != nil {
		return
	}
	f := informers.NewSharedInformerFactory(cs, 0)
	hook := func(scope string) cache.ResourceEventHandlerFuncs {
		return cache.ResourceEventHandlerFuncs{
			AddFunc:    func(obj any) { changed(scope, objectNamespace(obj)) },
			UpdateFunc: func(_, obj any) { changed(scope, objectNamespace(obj)) },
			DeleteFunc: func(obj any) { changed(scope, objectNamespace(obj)) },
		}
	}
	for _, is := range informerScopes {
		_, _ = is.informer(f).AddEventHandler(hook(is.scope))
	}
	k := NewCache(f)
	watches := []crdWatch{{gvr: httpRouteGVR, handler: hook(ScopeNetwork)}}
	for _, fk := range fluxKinds {
		watches = append(watches, crdWatch{gvr: fk.gvr, handler: hook(ScopeFlux), flux: true})
	}
	go c.watchCRDs(ctx, k, watches)
	c.UseCache(k)
	f.Start(ctx.Done())
	f.WaitForCacheSync(ctx.Done())
	<-ctx.Done()
	c.dropCache(k)
	f.Shutdown()
}

func objectNamespace(obj any) string {
	if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = d.Obj
	}
	if ns, ok := obj.(*corev1.Namespace); ok {
		return ns.Name
	}
	if o, ok := obj.(metav1.Object); ok {
		return o.GetNamespace()
	}
	return ""
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

type crdWatch struct {
	gvr     schema.GroupVersionResource
	handler cache.ResourceEventHandler
	flux    bool
}

func (c *Client) watchCRDs(ctx context.Context, tracked *Cache, watches []crdWatch) {
	cfg := c.streamConfig()
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return
	}
	meta, err := metadata.NewForConfig(cfg)
	if err != nil {
		return
	}
	byCRD := map[string]crdWatch{}
	for _, w := range watches {
		byCRD[w.gvr.Resource+"."+w.gvr.Group] = w
	}
	running := map[string]context.CancelFunc{}
	start := func(obj any) {
		name, _ := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
		w, ok := byCRD[name]
		if !ok || running[name] != nil {
			return
		}
		ictx, cancel := context.WithCancel(ctx)
		running[name] = cancel
		inf := dynamicinformer.NewFilteredDynamicInformer(dyn, w.gvr, "", 0, cache.Indexers{}, nil).Informer()
		_, _ = inf.AddEventHandler(w.handler)
		if w.flux {
			tracked.trackFlux(w.gvr, inf)
		}
		go inf.Run(ictx.Done())
	}
	stop := func(obj any) {
		name, _ := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
		if cancel := running[name]; cancel != nil {
			if w := byCRD[name]; w.flux {
				tracked.untrackFlux(w.gvr)
			}
			cancel()
			delete(running, name)
		}
	}
	crds := metadatainformer.NewFilteredMetadataInformer(meta, crdResource, "", 0, cache.Indexers{}, nil).Informer()
	reg, err := crds.AddEventHandler(cache.ResourceEventHandlerFuncs{AddFunc: start, DeleteFunc: stop})
	if err == nil {
		tracked.trackCRDs(reg.HasSynced)
	}
	crds.Run(ctx.Done())
}
