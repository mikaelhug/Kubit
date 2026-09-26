package k8s

import (
	"context"
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

const FieldManager = "kubit"

func (c *Client) ServerSideApply(ctx context.Context, objects []map[string]any) error {
	dyn, err := c.dynClient()
	if err != nil {
		return err
	}
	force := true
	for _, obj := range objects {
		u := &unstructured.Unstructured{Object: obj}
		gvk := u.GroupVersionKind()
		mapping, err := c.restMapping(gvk)
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

func (c *Client) restMapping(gvk schema.GroupVersionKind) (*meta.RESTMapping, error) {
	mapper := c.restMapper()
	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if meta.IsNoMatchError(err) {
		mapper.Reset()
		mapping, err = mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	}
	return mapping, err
}
