package k8s

import (
	"context"
	"sort"
	"sync"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
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

func NewCache(f informers.SharedInformerFactory) *Cache {
	k := &Cache{f: f}
	for _, inf := range []cache.SharedIndexInformer{
		f.Core().V1().Pods().Informer(),
		f.Core().V1().Namespaces().Informer(),
		f.Core().V1().Services().Informer(),
		f.Core().V1().PersistentVolumeClaims().Informer(),
		f.Core().V1().PersistentVolumes().Informer(),
		f.Apps().V1().Deployments().Informer(),
		f.Apps().V1().DaemonSets().Informer(),
		f.Apps().V1().StatefulSets().Informer(),
		f.Batch().V1().Jobs().Informer(),
		f.Batch().V1().CronJobs().Informer(),
		f.Discovery().V1().EndpointSlices().Informer(),
		f.Networking().V1().Ingresses().Informer(),
		f.Storage().V1().StorageClasses().Informer(),
	} {
		k.synced = append(k.synced, inf.HasSynced)
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

func (c *Client) deployments(ctx context.Context, ns string) ([]appsv1.Deployment, error) {
	if f := c.listers(); f != nil {
		return cached(f.Apps().V1().Deployments().Lister().Deployments(ns).List(labels.Everything()))
	}
	list, err := c.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (c *Client) daemonSets(ctx context.Context, ns string) ([]appsv1.DaemonSet, error) {
	if f := c.listers(); f != nil {
		return cached(f.Apps().V1().DaemonSets().Lister().DaemonSets(ns).List(labels.Everything()))
	}
	list, err := c.AppsV1().DaemonSets(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (c *Client) statefulSets(ctx context.Context, ns string) ([]appsv1.StatefulSet, error) {
	if f := c.listers(); f != nil {
		return cached(f.Apps().V1().StatefulSets().Lister().StatefulSets(ns).List(labels.Everything()))
	}
	list, err := c.AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (c *Client) jobs(ctx context.Context, ns string) ([]batchv1.Job, error) {
	if f := c.listers(); f != nil {
		return cached(f.Batch().V1().Jobs().Lister().Jobs(ns).List(labels.Everything()))
	}
	list, err := c.BatchV1().Jobs(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (c *Client) cronJobs(ctx context.Context) ([]batchv1.CronJob, error) {
	if f := c.listers(); f != nil {
		return cached(f.Batch().V1().CronJobs().Lister().List(labels.Everything()))
	}
	list, err := c.BatchV1().CronJobs("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
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
	if f := c.listers(); f != nil {
		return cached(f.Core().V1().Namespaces().Lister().List(labels.Everything()))
	}
	list, err := c.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (c *Client) services(ctx context.Context) ([]corev1.Service, error) {
	if f := c.listers(); f != nil {
		return cached(f.Core().V1().Services().Lister().List(labels.Everything()))
	}
	list, err := c.CoreV1().Services("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (c *Client) endpointSlices(ctx context.Context) ([]discoveryv1.EndpointSlice, error) {
	if f := c.listers(); f != nil {
		return cached(f.Discovery().V1().EndpointSlices().Lister().List(labels.Everything()))
	}
	list, err := c.DiscoveryV1().EndpointSlices("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (c *Client) ingresses(ctx context.Context) ([]networkingv1.Ingress, error) {
	if f := c.listers(); f != nil {
		return cached(f.Networking().V1().Ingresses().Lister().List(labels.Everything()))
	}
	list, err := c.NetworkingV1().Ingresses("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (c *Client) claims(ctx context.Context) ([]corev1.PersistentVolumeClaim, error) {
	if f := c.listers(); f != nil {
		return cached(f.Core().V1().PersistentVolumeClaims().Lister().List(labels.Everything()))
	}
	list, err := c.CoreV1().PersistentVolumeClaims("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (c *Client) volumes(ctx context.Context) ([]corev1.PersistentVolume, error) {
	if f := c.listers(); f != nil {
		return cached(f.Core().V1().PersistentVolumes().Lister().List(labels.Everything()))
	}
	list, err := c.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (c *Client) storageClasses(ctx context.Context) ([]storagev1.StorageClass, error) {
	if f := c.listers(); f != nil {
		return cached(f.Storage().V1().StorageClasses().Lister().List(labels.Everything()))
	}
	list, err := c.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}
