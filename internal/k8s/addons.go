package k8s

import (
	"context"
)

type Readiness struct {
	Namespace string   `json:"namespace"`
	Ready     int      `json:"ready"`
	Total     int      `json:"total"`
	Detail    []string `json:"detail,omitempty"`
}

func (c *Client) NamespaceReadiness(ctx context.Context, namespace string) (*Readiness, error) {
	r := &Readiness{Namespace: namespace}
	deps, err := c.deployments(ctx, namespace)
	if err != nil {
		return nil, err
	}
	for _, d := range deps {
		r.Total++
		if d.Spec.Replicas != nil && d.Status.AvailableReplicas == *d.Spec.Replicas {
			r.Ready++
		} else {
			r.Detail = append(r.Detail, "Deployment "+d.Name)
		}
	}
	dss, err := c.daemonSets(ctx, namespace)
	if err != nil {
		return nil, err
	}
	for _, d := range dss {
		r.Total++
		if d.Status.NumberReady == d.Status.DesiredNumberScheduled {
			r.Ready++
		} else {
			r.Detail = append(r.Detail, "DaemonSet "+d.Name)
		}
	}
	sts, err := c.statefulSets(ctx, namespace)
	if err != nil {
		return nil, err
	}
	for _, d := range sts {
		r.Total++
		if d.Spec.Replicas != nil && d.Status.ReadyReplicas == *d.Spec.Replicas {
			r.Ready++
		} else {
			r.Detail = append(r.Detail, "StatefulSet "+d.Name)
		}
	}
	return r, nil
}
