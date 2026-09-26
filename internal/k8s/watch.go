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
	ScopeServices  = "services"
	ScopeOffsite   = "offsite"
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
	k := NewCache(f)
	go c.watchFlux(ctx, k, hook(ScopeFlux))
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

func (c *Client) watchFlux(ctx context.Context, tracked *Cache, handler cache.ResourceEventHandler) {
	cfg := c.streamConfig()
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return
	}
	meta, err := metadata.NewForConfig(cfg)
	if err != nil {
		return
	}
	byCRD := map[string]schema.GroupVersionResource{}
	for _, k := range fluxKinds {
		byCRD[k.gvr.Resource+"."+k.gvr.Group] = k.gvr
	}
	running := map[string]context.CancelFunc{}
	start := func(obj any) {
		name, _ := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
		gvr, ok := byCRD[name]
		if !ok || running[name] != nil {
			return
		}
		ictx, cancel := context.WithCancel(ctx)
		running[name] = cancel
		inf := dynamicinformer.NewFilteredDynamicInformer(dyn, gvr, "", 0, cache.Indexers{}, nil).Informer()
		_, _ = inf.AddEventHandler(handler)
		tracked.trackFlux(gvr, inf)
		go inf.Run(ictx.Done())
	}
	stop := func(obj any) {
		name, _ := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
		if cancel := running[name]; cancel != nil {
			tracked.untrackFlux(byCRD[name])
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
