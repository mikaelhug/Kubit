package k8s

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
	if out, ok := c.cachedFluxObjects(); ok {
		return out, nil
	}
	dyn, err := c.dynClient()
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

func (c *Client) cachedFluxObjects() ([]FluxObject, bool) {
	k := c.cache.Load()
	if k == nil {
		return nil, false
	}
	stores, ok := k.fluxStores()
	if !ok {
		return nil, false
	}
	out := []FluxObject{}
	for _, kind := range fluxKinds {
		store := stores[kind.gvr]
		if store == nil {
			continue
		}
		var objs []*unstructured.Unstructured
		for _, obj := range store.List() {
			if u, ok := obj.(*unstructured.Unstructured); ok {
				objs = append(objs, u)
			}
		}
		for _, u := range sortedValues(objs) {
			out = append(out, fluxObject(kind.kind, u))
		}
	}
	return out, true
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
