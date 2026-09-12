package k8s

import (
	"context"
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/restmapper"
)

// FieldManager identifies Kubit's server-side applies.
const FieldManager = "kubit"

// ServerSideApply upserts arbitrary objects (Talos' rendered bootstrap manifests) the
// way `kubectl apply --server-side --force-conflicts` does.
func (c *Client) ServerSideApply(ctx context.Context, objects []map[string]any) error {
	dyn, err := dynamic.NewForConfig(c.rest)
	if err != nil {
		return err
	}
	groups, err := restmapper.GetAPIGroupResources(c.Discovery())
	if err != nil {
		return err
	}
	mapper := restmapper.NewDiscoveryRESTMapper(groups)
	force := true
	for _, obj := range objects {
		u := &unstructured.Unstructured{Object: obj}
		gvk := u.GroupVersionKind()
		mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			return fmt.Errorf("%s %s: %w", gvk.Kind, u.GetName(), err)
		}
		var res dynamic.ResourceInterface = dyn.Resource(mapping.Resource)
		if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
			ns := u.GetNamespace()
			if ns == "" {
				ns = "default"
			}
			res = dyn.Resource(mapping.Resource).Namespace(ns)
		}
		body, err := json.Marshal(u.Object)
		if err != nil {
			return err
		}
		if _, err := res.Patch(ctx, u.GetName(), types.ApplyPatchType, body, metav1.PatchOptions{FieldManager: FieldManager, Force: &force}); err != nil {
			return fmt.Errorf("apply %s %s: %w", gvk.Kind, u.GetName(), err)
		}
	}
	return nil
}
