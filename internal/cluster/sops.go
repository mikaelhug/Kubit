package cluster

import (
	"context"
	"encoding/base64"
	"fmt"

	"filippo.io/age"
	"github.com/mikaelhug/kubit/internal/config"
	"github.com/mikaelhug/kubit/internal/tofu"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type SOPSKey struct {
	Identity  []byte `json:"-"`
	Recipient string `json:"recipient"`
}

func (m *Manager) SOPSKey(name string) (*SOPSKey, error) {
	d, err := m.Desired(name)
	if err != nil {
		return nil, err
	}
	if d.FluxKey == "" {
		return nil, fmt.Errorf("%s has no flux.ageKey", name)
	}
	id, err := age.ParseX25519Identity(d.FluxKey)
	if err != nil {
		return nil, fmt.Errorf("flux.ageKey: %w", err)
	}
	return &SOPSKey{Identity: []byte(id.String() + "\n"), Recipient: id.Recipient().String()}, nil
}

func (m *Manager) installSOPSKey(ctx context.Context, c *config.Cluster, sink Sink) error {
	name := c.Metadata.Name
	kc, err := m.KubeClient(ctx, name)
	if err != nil {
		return err
	}
	if !c.Spec.Platform.Flux.Enabled {
		err := kc.CoreV1().Secrets(tofu.SOPSNamespace).Delete(ctx, tofu.SOPSSecret, metav1.DeleteOptions{})
		if err == nil {
			sink.Emit(Info, "apply", "", "removed the SOPS key from the cluster")
		}
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("remove SOPS key: %w", err)
		}
		return nil
	}
	k, err := m.SOPSKey(name)
	if err != nil {
		return err
	}
	objects := []map[string]any{
		{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": tofu.SOPSNamespace}},
		{
			"apiVersion": "v1", "kind": "Secret", "type": "Opaque",
			"metadata": map[string]any{"name": tofu.SOPSSecret, "namespace": tofu.SOPSNamespace, "labels": map[string]any{"app.kubernetes.io/managed-by": "kubit"}},
			"data":     map[string]any{tofu.SOPSSecretKey: base64.StdEncoding.EncodeToString(k.Identity)},
		},
	}
	if err := kc.ServerSideApply(ctx, objects); err != nil {
		return fmt.Errorf("install SOPS key: %w", err)
	}
	sink.Emit(Info, "apply", "", "SOPS key installed (%s)", k.Recipient)
	return nil
}
