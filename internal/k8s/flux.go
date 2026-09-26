package k8s

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/metadata/metadatainformer"
	"k8s.io/client-go/tools/cache"
)

type FluxObject struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Ready     string `json:"ready"`
	Reason    string `json:"reason,omitempty"`
	Message   string `json:"message,omitempty"`
	Revision  string `json:"revision,omitempty"`
	Suspended bool   `json:"suspended,omitempty"`
	Since     string `json:"since,omitempty"`
}

var fluxKinds = []struct {
	kind string
	gvr  schema.GroupVersionResource
}{
	{"GitRepository", schema.GroupVersionResource{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "gitrepositories"}},
	{"OCIRepository", schema.GroupVersionResource{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "ocirepositories"}},
	{"HelmRepository", schema.GroupVersionResource{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "helmrepositories"}},
	{"Kustomization", schema.GroupVersionResource{Group: "kustomize.toolkit.fluxcd.io", Version: "v1", Resource: "kustomizations"}},
	{"HelmRelease", schema.GroupVersionResource{Group: "helm.toolkit.fluxcd.io", Version: "v2", Resource: "helmreleases"}},
}

var crdResource = schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}

func (c *Client) FluxObjects(ctx context.Context) ([]FluxObject, error) {
	dyn, err := dynamic.NewForConfig(c.rest)
	if err != nil {
		return nil, err
	}
	out := []FluxObject{}
	for _, k := range fluxKinds {
		list, err := dyn.Resource(k.gvr).List(ctx, metav1.ListOptions{})
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, u := range list.Items {
			out = append(out, fluxObject(k.kind, u))
		}
	}
	return out, nil
}

func fluxObject(kind string, u unstructured.Unstructured) FluxObject {
	o := FluxObject{Kind: kind, Namespace: u.GetNamespace(), Name: u.GetName(), Ready: "Unknown"}
	o.Suspended, _, _ = unstructured.NestedBool(u.Object, "spec", "suspend")
	switch kind {
	case "GitRepository", "OCIRepository", "HelmRepository":
		o.Revision, _, _ = unstructured.NestedString(u.Object, "status", "artifact", "revision")
	case "Kustomization":
		o.Revision, _, _ = unstructured.NestedString(u.Object, "status", "lastAppliedRevision")
	case "HelmRelease":
		if h, _, _ := unstructured.NestedSlice(u.Object, "status", "history"); len(h) > 0 {
			if m, ok := h[0].(map[string]any); ok {
				o.Revision, _ = m["chartVersion"].(string)
			}
		}
	}
	conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	for _, c := range conds {
		m, ok := c.(map[string]any)
		if !ok || m["type"] != "Ready" {
			continue
		}
		o.Ready, _ = m["status"].(string)
		o.Reason, _ = m["reason"].(string)
		o.Message, _ = m["message"].(string)
		o.Since, _ = m["lastTransitionTime"].(string)
	}
	return o
}

func (c *Client) watchFlux(ctx context.Context, handler cache.ResourceEventHandler) {
	dyn, err := dynamic.NewForConfig(c.rest)
	if err != nil {
		return
	}
	meta, err := metadata.NewForConfig(c.rest)
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
		go inf.Run(ictx.Done())
	}
	stop := func(obj any) {
		name, _ := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
		if cancel := running[name]; cancel != nil {
			cancel()
			delete(running, name)
		}
	}
	crds := metadatainformer.NewFilteredMetadataInformer(meta, crdResource, "", 0, cache.Indexers{}, nil).Informer()
	_, _ = crds.AddEventHandler(cache.ResourceEventHandlerFuncs{AddFunc: start, DeleteFunc: stop})
	crds.Run(ctx.Done())
}
