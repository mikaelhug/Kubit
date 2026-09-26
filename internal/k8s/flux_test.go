package k8s

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestFluxObject(t *testing.T) {
	ks := unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "flux-system", "namespace": "flux-system"},
		"spec":     map[string]any{"suspend": true},
		"status": map[string]any{
			"lastAppliedRevision": "main@sha1:0123456789abcdef",
			"conditions": []any{
				map[string]any{"type": "Reconciling", "status": "False"},
				map[string]any{"type": "Ready", "status": "False", "reason": "BuildFailed", "message": "kustomize build failed", "lastTransitionTime": "2026-09-26T10:00:00Z"},
			},
		},
	}}
	got := fluxObject("Kustomization", ks)
	want := FluxObject{Kind: "Kustomization", Namespace: "flux-system", Name: "flux-system", Ready: "False", Reason: "BuildFailed", Message: "kustomize build failed", Revision: "main@sha1:0123456789abcdef", Suspended: true, Since: "2026-09-26T10:00:00Z"}
	if got != want {
		t.Errorf("kustomization = %+v", got)
	}

	hr := unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "podinfo", "namespace": "podinfo"},
		"status":   map[string]any{"history": []any{map[string]any{"chartVersion": "6.15.0"}, map[string]any{"chartVersion": "6.14.0"}}},
	}}
	if got := fluxObject("HelmRelease", hr); got.Revision != "6.15.0" || got.Ready != "Unknown" {
		t.Errorf("helmrelease = %+v", got)
	}

	gr := unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "flux-system", "namespace": "flux-system"},
		"status":   map[string]any{"artifact": map[string]any{"revision": "main@sha1:fedcba"}, "conditions": []any{map[string]any{"type": "Ready", "status": "True"}}},
	}}
	if got := fluxObject("GitRepository", gr); got.Revision != "main@sha1:fedcba" || got.Ready != "True" {
		t.Errorf("gitrepository = %+v", got)
	}

	oci := unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "podinfo", "namespace": "bobinfo"},
		"status":   map[string]any{"artifact": map[string]any{"revision": "6.15.0@sha256:abc"}, "conditions": []any{map[string]any{"type": "Ready", "status": "False", "message": "failed to determine artifact digest"}}},
	}}
	if got := fluxObject("OCIRepository", oci); got.Revision != "6.15.0@sha256:abc" || got.Ready != "False" {
		t.Errorf("ocirepository = %+v", got)
	}
}
