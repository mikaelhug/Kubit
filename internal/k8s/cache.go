package k8s

import (
	"context"
	"fmt"
	"sort"
	"sync"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
)

type Cache struct {
	f      informers.SharedInformerFactory
	synced []cache.InformerSynced

	fluxMu sync.Mutex
	crds   cache.InformerSynced
	flux   map[schema.GroupVersionResource]cache.SharedIndexInformer
}

type informerScope struct {
	gvr   schema.GroupVersionResource
	scope string
}

var informerScopes = []informerScope{
	{corev1.SchemeGroupVersion.WithResource("pods"), ScopeWorkloads},
	{appsv1.SchemeGroupVersion.WithResource("deployments"), ScopeWorkloads},
	{appsv1.SchemeGroupVersion.WithResource("daemonsets"), ScopeWorkloads},
	{appsv1.SchemeGroupVersion.WithResource("statefulsets"), ScopeWorkloads},
	{batchv1.SchemeGroupVersion.WithResource("jobs"), ScopeWorkloads},
	{batchv1.SchemeGroupVersion.WithResource("cronjobs"), ScopeWorkloads},
	{corev1.SchemeGroupVersion.WithResource("namespaces"), ScopeWorkloads},
	{corev1.SchemeGroupVersion.WithResource("services"), ScopeNetwork},
	{discoveryv1.SchemeGroupVersion.WithResource("endpointslices"), ScopeNetwork},
	{networkingv1.SchemeGroupVersion.WithResource("ingresses"), ScopeNetwork},
	{corev1.SchemeGroupVersion.WithResource("persistentvolumeclaims"), ScopeStorage},
	{corev1.SchemeGroupVersion.WithResource("persistentvolumes"), ScopeStorage},
	{storagev1.SchemeGroupVersion.WithResource("storageclasses"), ScopeStorage},
	{corev1.SchemeGroupVersion.WithResource("nodes"), ScopeNodes},
}

func (is informerScope) informer(f informers.SharedInformerFactory) cache.SharedIndexInformer {
	g, err := f.ForResource(is.gvr)
	if err != nil {
		panic(err)
	}
	return g.Informer()
}

func NewCache(f informers.SharedInformerFactory) *Cache {
	k := &Cache{f: f}
	for _, is := range informerScopes {
		k.synced = append(k.synced, is.informer(f).HasSynced)
	}
	return k
}

func (k *Cache) HasSynced() bool {
	for _, synced := range k.synced {
		if !synced() {
			return false
		}
	}
	return true
}

func (k *Cache) trackCRDs(synced cache.InformerSynced) {
	k.fluxMu.Lock()
	defer k.fluxMu.Unlock()
	k.crds = synced
}

func (k *Cache) trackFlux(gvr schema.GroupVersionResource, inf cache.SharedIndexInformer) {
	k.fluxMu.Lock()
	defer k.fluxMu.Unlock()
	if k.flux == nil {
		k.flux = map[schema.GroupVersionResource]cache.SharedIndexInformer{}
	}
	k.flux[gvr] = inf
}

func (k *Cache) untrackFlux(gvr schema.GroupVersionResource) {
	k.fluxMu.Lock()
	defer k.fluxMu.Unlock()
	delete(k.flux, gvr)
}

func (k *Cache) fluxStores() (map[schema.GroupVersionResource]cache.Store, bool) {
	k.fluxMu.Lock()
	defer k.fluxMu.Unlock()
	if k.crds == nil || !k.crds() {
		return nil, false
	}
	stores := map[schema.GroupVersionResource]cache.Store{}
	for gvr, inf := range k.flux {
		if !inf.HasSynced() {
			return nil, false
		}
		stores[gvr] = inf.GetStore()
	}
	return stores, true
}

func (c *Client) UseCache(k *Cache) { c.cache.Store(k) }

func (c *Client) dropCache(k *Cache) { c.cache.CompareAndSwap(k, nil) }

func (c *Client) listers() informers.SharedInformerFactory {
	if k := c.cache.Load(); k != nil && k.HasSynced() {
		return k.f
	}
	return nil
}

func sortedValues[T any, P interface {
	*T
	metav1.Object
}](objs []P) []T {
	sort.Slice(objs, func(i, j int) bool { return objectKey(objs[i]) < objectKey(objs[j]) })
	out := make([]T, len(objs))
	for i, o := range objs {
		out[i] = *o
	}
	return out
}

func objectKey(o metav1.Object) string {
	if ns := o.GetNamespace(); ns != "" {
		return ns + "/" + o.GetName()
	}
	return o.GetName()
}

func cached[T any, P interface {
	*T
	metav1.Object
}](objs []P, err error) ([]T, error) {
	if err != nil {
		return nil, err
	}
	return sortedValues(objs), nil
}

func cachedOr[T any, P interface {
	*T
	metav1.Object
}](c *Client, lister func(informers.SharedInformerFactory) ([]P, error), live func() (runtime.Object, error)) ([]T, error) {
	if f := c.listers(); f != nil {
		return cached(lister(f))
	}
	list, err := live()
	if err != nil {
		return nil, err
	}
	objs, err := meta.ExtractList(list)
	if err != nil {
		return nil, err
	}
	out := make([]T, 0, len(objs))
	for _, o := range objs {
		p, ok := o.(P)
		if !ok {
			return nil, fmt.Errorf("unexpected %T in %T", o, list)
		}
		out = append(out, *p)
	}
	return out, nil
}

var listAll = metav1.ListOptions{}

func (c *Client) deployments(ctx context.Context, ns string) ([]appsv1.Deployment, error) {
	return cachedOr(c, func(f informers.SharedInformerFactory) ([]*appsv1.Deployment, error) {
		return f.Apps().V1().Deployments().Lister().Deployments(ns).List(labels.Everything())
	}, func() (runtime.Object, error) { return c.AppsV1().Deployments(ns).List(ctx, listAll) })
}

func (c *Client) daemonSets(ctx context.Context, ns string) ([]appsv1.DaemonSet, error) {
	return cachedOr(c, func(f informers.SharedInformerFactory) ([]*appsv1.DaemonSet, error) {
		return f.Apps().V1().DaemonSets().Lister().DaemonSets(ns).List(labels.Everything())
	}, func() (runtime.Object, error) { return c.AppsV1().DaemonSets(ns).List(ctx, listAll) })
}

func (c *Client) statefulSets(ctx context.Context, ns string) ([]appsv1.StatefulSet, error) {
	return cachedOr(c, func(f informers.SharedInformerFactory) ([]*appsv1.StatefulSet, error) {
		return f.Apps().V1().StatefulSets().Lister().StatefulSets(ns).List(labels.Everything())
	}, func() (runtime.Object, error) { return c.AppsV1().StatefulSets(ns).List(ctx, listAll) })
}

func (c *Client) jobs(ctx context.Context, ns string) ([]batchv1.Job, error) {
	return cachedOr(c, func(f informers.SharedInformerFactory) ([]*batchv1.Job, error) {
		return f.Batch().V1().Jobs().Lister().Jobs(ns).List(labels.Everything())
	}, func() (runtime.Object, error) { return c.BatchV1().Jobs(ns).List(ctx, listAll) })
}

func (c *Client) cronJobs(ctx context.Context) ([]batchv1.CronJob, error) {
	return cachedOr(c, func(f informers.SharedInformerFactory) ([]*batchv1.CronJob, error) {
		return f.Batch().V1().CronJobs().Lister().List(labels.Everything())
	}, func() (runtime.Object, error) { return c.BatchV1().CronJobs("").List(ctx, listAll) })
}

func (c *Client) pods(ctx context.Context, ns string, opts metav1.ListOptions) ([]corev1.Pod, error) {
	if f := c.listers(); f != nil {
		sel, lerr := labels.Parse(opts.LabelSelector)
		fsel, ferr := fields.ParseSelector(opts.FieldSelector)
		if lerr == nil && ferr == nil {
			objs, err := f.Core().V1().Pods().Lister().Pods(ns).List(sel)
			if err != nil {
				return nil, err
			}
			matched := objs[:0]
			for _, p := range objs {
				if fsel.Matches(fields.Set{"spec.nodeName": p.Spec.NodeName, "status.phase": string(p.Status.Phase)}) {
					matched = append(matched, p)
				}
			}
			return sortedValues(matched), nil
		}
	}
	list, err := c.CoreV1().Pods(ns).List(ctx, opts)
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (c *Client) namespaces(ctx context.Context) ([]corev1.Namespace, error) {
	return cachedOr(c, func(f informers.SharedInformerFactory) ([]*corev1.Namespace, error) {
		return f.Core().V1().Namespaces().Lister().List(labels.Everything())
	}, func() (runtime.Object, error) { return c.CoreV1().Namespaces().List(ctx, listAll) })
}

func (c *Client) services(ctx context.Context) ([]corev1.Service, error) {
	return cachedOr(c, func(f informers.SharedInformerFactory) ([]*corev1.Service, error) {
		return f.Core().V1().Services().Lister().List(labels.Everything())
	}, func() (runtime.Object, error) { return c.CoreV1().Services("").List(ctx, listAll) })
}

func (c *Client) endpointSlices(ctx context.Context) ([]discoveryv1.EndpointSlice, error) {
	return cachedOr(c, func(f informers.SharedInformerFactory) ([]*discoveryv1.EndpointSlice, error) {
		return f.Discovery().V1().EndpointSlices().Lister().List(labels.Everything())
	}, func() (runtime.Object, error) { return c.DiscoveryV1().EndpointSlices("").List(ctx, listAll) })
}

func (c *Client) ingresses(ctx context.Context) ([]networkingv1.Ingress, error) {
	return cachedOr(c, func(f informers.SharedInformerFactory) ([]*networkingv1.Ingress, error) {
		return f.Networking().V1().Ingresses().Lister().List(labels.Everything())
	}, func() (runtime.Object, error) { return c.NetworkingV1().Ingresses("").List(ctx, listAll) })
}

func (c *Client) claims(ctx context.Context) ([]corev1.PersistentVolumeClaim, error) {
	return cachedOr(c, func(f informers.SharedInformerFactory) ([]*corev1.PersistentVolumeClaim, error) {
		return f.Core().V1().PersistentVolumeClaims().Lister().List(labels.Everything())
	}, func() (runtime.Object, error) { return c.CoreV1().PersistentVolumeClaims("").List(ctx, listAll) })
}

func (c *Client) volumes(ctx context.Context) ([]corev1.PersistentVolume, error) {
	return cachedOr(c, func(f informers.SharedInformerFactory) ([]*corev1.PersistentVolume, error) {
		return f.Core().V1().PersistentVolumes().Lister().List(labels.Everything())
	}, func() (runtime.Object, error) { return c.CoreV1().PersistentVolumes().List(ctx, listAll) })
}

func (c *Client) storageClasses(ctx context.Context) ([]storagev1.StorageClass, error) {
	return cachedOr(c, func(f informers.SharedInformerFactory) ([]*storagev1.StorageClass, error) {
		return f.Storage().V1().StorageClasses().Lister().List(labels.Everything())
	}, func() (runtime.Object, error) { return c.StorageV1().StorageClasses().List(ctx, listAll) })
}

func (c *Client) nodes(ctx context.Context) ([]corev1.Node, error) {
	return cachedOr(c, func(f informers.SharedInformerFactory) ([]*corev1.Node, error) {
		return f.Core().V1().Nodes().Lister().List(labels.Everything())
	}, func() (runtime.Object, error) { return c.CoreV1().Nodes().List(ctx, listAll) })
}

func (c *Client) node(ctx context.Context, name string) (*corev1.Node, error) {
	if f := c.listers(); f != nil {
		n, err := f.Core().V1().Nodes().Lister().Get(name)
		if err != nil {
			return nil, err
		}
		return n.DeepCopy(), nil
	}
	return c.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
}
