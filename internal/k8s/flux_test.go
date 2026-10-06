package k8s

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
)

func fluxResource(kind, ns, name string) *unstructured.Unstructured {
	for _, k := range fluxKinds {
		if k.kind == kind {
			u := &unstructured.Unstructured{}
			u.SetAPIVersion(k.gvr.GroupVersion().String())
			u.SetKind(kind)
			u.SetNamespace(ns)
			u.SetName(name)
			return u
		}
	}
	panic(kind)
}

func fluxClient(objs ...runtime.Object) dynamic.Interface {
	lists := map[schema.GroupVersionResource]string{}
	for _, k := range fluxKinds {
		lists[k.gvr] = k.kind + "List"
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), lists, objs...)
}

func fluxKeys(objs []FluxObject) []string {
	var out []string
	for _, o := range objs {
		out = append(out, o.Kind+" "+o.Namespace+"/"+o.Name)
	}
	return out
}

func TestFluxObjectsFromTheWatcherInformers(t *testing.T) {
	ctx := context.Background()
	c := &Client{}
	c.dynOnce.Do(func() { c.dyn = fluxClient(fluxResource("Kustomization", "flux-system", "live")) })
	live := []string{"Kustomization flux-system/live"}
	if got, err := c.FluxObjects(ctx); err != nil || len(got) != 1 || fluxKeys(got)[0] != live[0] {
		t.Fatalf("no cache: %v %v", fluxKeys(got), err)
	}

	k := NewCache(informers.NewSharedInformerFactory(fake.NewClientset(), 0))
	c.UseCache(k)
	crdsSynced := false
	k.trackCRDs(func() bool { return crdsSynced })
	if got, _ := c.FluxObjects(ctx); len(got) != 1 || fluxKeys(got)[0] != live[0] {
		t.Fatalf("unsynced CRDs must fall back to live reads: %v", fluxKeys(got))
	}

	watched := fluxClient(
		fluxResource("Kustomization", "flux-system", "b"),
		fluxResource("Kustomization", "apps", "z"),
		fluxResource("Kustomization", "flux-system", "a"),
		fluxResource("GitRepository", "flux-system", "flux-system"),
	)
	stop := make(chan struct{})
	defer close(stop)
	var infs []cache.SharedIndexInformer
	for _, kind := range []string{"Kustomization", "GitRepository"} {
		for _, fk := range fluxKinds {
			if fk.kind == kind {
				inf := dynamicinformer.NewFilteredDynamicInformer(watched, fk.gvr, "", 0, cache.Indexers{}, nil).Informer()
				k.trackFlux(fk.gvr, inf)
				infs = append(infs, inf)
			}
		}
	}
	crdsSynced = true
	if got, _ := c.FluxObjects(ctx); len(got) != 1 || fluxKeys(got)[0] != live[0] {
		t.Fatalf("unsynced informers must fall back to live reads: %v", fluxKeys(got))
	}

	for _, inf := range infs {
		go inf.Run(stop)
	}
	for _, inf := range infs {
		cache.WaitForCacheSync(stop, inf.HasSynced)
	}
	got, err := c.FluxObjects(ctx)
	if err != nil || len(got) != 4 {
		t.Fatalf("synced informers: %v %v", fluxKeys(got), err)
	}

	c.dropCache(k)
	if got, _ := c.FluxObjects(ctx); len(got) != 1 || fluxKeys(got)[0] != live[0] {
		t.Errorf("a dropped cache must fall back to live reads: %v", fluxKeys(got))
	}
}
