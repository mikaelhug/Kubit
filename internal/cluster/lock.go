package cluster

import (
	"context"
	"fmt"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	applyLease     = "kubit-apply"
	applyLeaseTime = time.Hour
)

func (m *Manager) LockApply(ctx context.Context, d *Desired, holder string) (func(), error) {
	kc, err := m.KubeClientFor(d.Cluster.Metadata.Name, d.Kubeconfig)
	if err != nil {
		return func() {}, nil
	}
	leases := kc.CoordinationV1().Leases("kube-system")
	now := metav1.NewMicroTime(time.Now())
	secs := int32(applyLeaseTime / time.Second)
	spec := coordinationv1.LeaseSpec{HolderIdentity: &holder, LeaseDurationSeconds: &secs, AcquireTime: &now, RenewTime: &now}
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err = leases.Create(call, &coordinationv1.Lease{ObjectMeta: metav1.ObjectMeta{Name: applyLease}, Spec: spec}, metav1.CreateOptions{})
	switch {
	case apierrors.IsAlreadyExists(err):
		cur, err := leases.Get(call, applyLease, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		if held(cur, time.Now()) {
			return nil, fmt.Errorf("another kubit apply (%s) holds the cluster since %s", deref(cur.Spec.HolderIdentity), cur.Spec.AcquireTime.Format(time.RFC3339))
		}
		cur.Spec = spec
		if _, err := leases.Update(call, cur, metav1.UpdateOptions{}); err != nil {
			return nil, err
		}
	case err != nil:
		return func() {}, nil
	}
	return func() {
		release, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		cur, err := leases.Get(release, applyLease, metav1.GetOptions{})
		if err != nil || deref(cur.Spec.HolderIdentity) != holder {
			return
		}
		ended := metav1.NewMicroTime(time.Now())
		cur.Spec.HolderIdentity, cur.Spec.RenewTime = nil, &ended
		_, _ = leases.Update(release, cur, metav1.UpdateOptions{})
	}, nil
}

func (m *Manager) ApplyQuiet(ctx context.Context, name string, after time.Duration) bool {
	kc, err := m.KubeClient(ctx, name)
	if err != nil {
		return false
	}
	call, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	l, err := kc.CoordinationV1().Leases("kube-system").Get(call, applyLease, metav1.GetOptions{})
	if err != nil || l.Spec.RenewTime == nil {
		return false
	}
	now := time.Now()
	return held(l, now) || (deref(l.Spec.HolderIdentity) == "" && now.Sub(l.Spec.RenewTime.Time) < after)
}

func held(l *coordinationv1.Lease, now time.Time) bool {
	if deref(l.Spec.HolderIdentity) == "" || l.Spec.RenewTime == nil || l.Spec.LeaseDurationSeconds == nil {
		return false
	}
	return now.Before(l.Spec.RenewTime.Add(time.Duration(*l.Spec.LeaseDurationSeconds) * time.Second))
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (m *Manager) ApplyHolder(ctx context.Context, name string) (string, bool) {
	kc, err := m.KubeClient(ctx, name)
	if err != nil {
		return "", false
	}
	call, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	l, err := kc.CoordinationV1().Leases("kube-system").Get(call, applyLease, metav1.GetOptions{})
	if err != nil || !held(l, time.Now()) {
		return "", false
	}
	return deref(l.Spec.HolderIdentity), true
}

func (m *Manager) WaitApplyReleased(ctx context.Context, name string) error {
	kc, err := m.KubeClient(ctx, name)
	if err != nil {
		return err
	}
	leases := kc.CoordinationV1().Leases("kube-system")
	for {
		l, err := leases.Get(ctx, applyLease, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		now := time.Now()
		if !held(l, now) {
			return nil
		}
		w, err := kc.WatchLease(ctx, "kube-system", applyLease, l.ResourceVersion)
		if err != nil {
			return err
		}
		expiry := time.NewTimer(l.Spec.RenewTime.Add(time.Duration(*l.Spec.LeaseDurationSeconds) * time.Second).Sub(now))
		select {
		case <-ctx.Done():
		case <-expiry.C:
		case <-w.ResultChan():
		}
		expiry.Stop()
		w.Stop()
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
}
