package k8s

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Readiness sums the controllers in a namespace: how many are fully available.
type Readiness struct {
	Namespace string `json:"namespace"`
	Ready     int    `json:"ready"`
	Total     int    `json:"total"`
	// Detail lists controllers that are not fully available.
	Detail []string `json:"detail,omitempty"`
}

func (c *Client) NamespaceReadiness(ctx context.Context, namespace string) (*Readiness, error) {
	r := &Readiness{Namespace: namespace}
	deps, err := c.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for _, d := range deps.Items {
		r.Total++
		if d.Spec.Replicas != nil && d.Status.AvailableReplicas == *d.Spec.Replicas {
			r.Ready++
		} else {
			r.Detail = append(r.Detail, "Deployment "+d.Name)
		}
	}
	dss, err := c.AppsV1().DaemonSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for _, d := range dss.Items {
		r.Total++
		if d.Status.NumberReady == d.Status.DesiredNumberScheduled {
			r.Ready++
		} else {
			r.Detail = append(r.Detail, "DaemonSet "+d.Name)
		}
	}
	sts, err := c.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for _, d := range sts.Items {
		r.Total++
		if d.Spec.Replicas != nil && d.Status.ReadyReplicas == *d.Spec.Replicas {
			r.Ready++
		} else {
			r.Detail = append(r.Detail, "StatefulSet "+d.Name)
		}
	}
	return r, nil
}
