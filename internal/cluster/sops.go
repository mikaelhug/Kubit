package cluster

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"filippo.io/age"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/tofu"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (m *Manager) SOPSKey(ctx context.Context, name string) (*store.SOPSKey, error) {
	if _, err := m.Store.GetCluster(ctx, name); err != nil {
		return nil, err
	}
	k, err := m.Store.GetSOPSKey(ctx, name)
	if !errors.Is(err, store.ErrNotFound) {
		return k, err
	}
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, err
	}
	if err := m.Store.CreateSOPSKey(ctx, name, keysFile(id), id.Recipient().String()); err != nil {
		return nil, err
	}
	return m.Store.GetSOPSKey(ctx, name)
}

func (m *Manager) ImportSOPSKey(ctx context.Context, name string, keys []byte) (*store.SOPSKey, error) {
	if _, err := m.Store.GetCluster(ctx, name); err != nil {
		return nil, err
	}
	ids, err := age.ParseIdentities(bytes.NewReader(keys))
	if err != nil {
		return nil, fmt.Errorf("not an age key file: %w", err)
	}
	if len(ids) != 1 {
		return nil, fmt.Errorf("the key file holds %d keys; import exactly one", len(ids))
	}
	id, ok := ids[0].(*age.X25519Identity)
	if !ok {
		return nil, fmt.Errorf("only X25519 age keys (AGE-SECRET-KEY-1…) are supported")
	}
	if err := m.Store.PutSOPSKey(ctx, name, keysFile(id), id.Recipient().String()); err != nil {
		return nil, err
	}
	return m.Store.GetSOPSKey(ctx, name)
}

func keysFile(id *age.X25519Identity) []byte {
	return fmt.Appendf(nil, "# created: %s\n# public key: %s\n%s\n", time.Now().UTC().Format(time.RFC3339), id.Recipient(), id)
}

func (m *Manager) installSOPSKey(ctx context.Context, name string, sink Sink) error {
	c, _, err := m.LoadCluster(ctx, name)
	if err != nil {
		return err
	}
	kc, err := m.KubeClient(ctx, name)
	if err != nil {
		return err
	}
	if !c.Spec.Platform.Flux.Enabled {
		err := kc.CoreV1().Secrets(tofu.SOPSNamespace).Delete(ctx, tofu.SOPSSecret, metav1.DeleteOptions{})
		if err == nil {
			sink.emit(Info, "apply", "", "removed the SOPS key from the cluster")
		}
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("remove SOPS key: %w", err)
		}
		return nil
	}
	k, err := m.SOPSKey(ctx, name)
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
	sink.emit(Info, "apply", "", "SOPS key installed (%s)", k.Recipient)
	return nil
}
