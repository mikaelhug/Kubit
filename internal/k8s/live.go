package k8s

import (
	"context"
	"slices"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const LabelControlPlane = "node-role.kubernetes.io/control-plane"

var metallbPoolGVR = schema.GroupVersionResource{Group: "metallb.io", Version: "v1beta1", Resource: "ipaddresspools"}

type LoadBalancer struct {
	Enabled bool     `json:"enabled"`
	Pool    []string `json:"pool,omitempty"`
	IP      string   `json:"ip,omitempty"`
}

type Backup struct {
	Schedule     string `json:"schedule"`
	LastSchedule string `json:"lastSchedule,omitempty"`
	LastSuccess  string `json:"lastSuccess,omitempty"`
	Failed       bool   `json:"failed,omitempty"`
}

type CNI struct {
	Name     string `json:"name,omitempty"`
	Policies bool   `json:"policies"`
}

func (c *Client) LoadBalancer(ctx context.Context) LoadBalancer {
	var lb LoadBalancer
	if svcs, err := c.services(ctx); err == nil {
		for _, svc := range svcs {
			if svc.Namespace != "traefik" || svc.Name != "traefik" {
				continue
			}
			lb.Enabled = svc.Spec.Type == corev1.ServiceTypeLoadBalancer
			for _, in := range svc.Status.LoadBalancer.Ingress {
				if in.IP != "" {
					lb.IP = in.IP
					break
				}
			}
		}
	}
	pools, _ := c.crdObjects(ctx, metallbPoolGVR, "metallb-system")
	for _, p := range pools {
		if p.GetName() == "default" {
			lb.Pool, _, _ = unstructured.NestedStringSlice(p.Object, "spec", "addresses")
		}
	}
	return lb
}

func (c *Client) Backup(ctx context.Context) *Backup {
	crons, err := c.cronJobs(ctx)
	if err != nil {
		return nil
	}
	i := slices.IndexFunc(crons, func(cj batchv1.CronJob) bool { return cj.Namespace == "talos-backup" && cj.Name == "talos-backup" })
	if i < 0 {
		return nil
	}
	cj := crons[i]
	b := &Backup{Schedule: cj.Spec.Schedule}
	if t := cj.Status.LastScheduleTime; t != nil {
		b.LastSchedule = t.UTC().Format(time.RFC3339)
	}
	if t := cj.Status.LastSuccessfulTime; t != nil {
		b.LastSuccess = t.UTC().Format(time.RFC3339)
	}
	jobs, err := c.jobs(ctx, cj.Namespace)
	if err != nil {
		return b
	}
	var latest *batchv1.Job
	for j := range jobs {
		if !slices.ContainsFunc(jobs[j].OwnerReferences, func(o metav1.OwnerReference) bool { return o.Kind == "CronJob" && o.Name == cj.Name }) {
			continue
		}
		if latest == nil || jobs[j].CreationTimestamp.After(latest.CreationTimestamp.Time) {
			latest = &jobs[j]
		}
	}
	if latest != nil {
		b.Failed = latest.Status.Failed > 0 && latest.Status.Succeeded == 0 && latest.Status.Active == 0
	}
	return b
}

func (c *Client) CNI(ctx context.Context) CNI {
	var out CNI
	ds, err := c.daemonSets(ctx, "kube-system")
	if err != nil {
		return out
	}
	for _, d := range ds {
		if !strings.Contains(d.Name, "flannel") {
			continue
		}
		out.Name = strings.TrimPrefix(d.Name, "kube-")
		for _, ctr := range d.Spec.Template.Spec.Containers {
			if strings.Contains(ctr.Name, "network-policies") {
				out.Policies = true
			}
		}
	}
	return out
}

func (c *Client) ManifestAddonInstalled(ctx context.Context, key string) bool {
	switch key {
	case "gvisor":
		_, err := c.NodeV1().RuntimeClasses().Get(ctx, "gvisor", metav1.GetOptions{})
		return err == nil
	case "backup":
		return c.Backup(ctx) != nil
	}
	return false
}
