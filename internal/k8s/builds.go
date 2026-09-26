package k8s

import (
	"context"
	"sort"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const BuildsNamespace = "kubit-builds"

type Build struct {
	Name       string `json:"name"`
	Image      string `json:"image,omitempty"`
	State      string `json:"state"`
	Pod        string `json:"pod,omitempty"`
	StartedAt  string `json:"startedAt,omitempty"`
	FinishedAt string `json:"finishedAt,omitempty"`
}

func (c *Client) Builds(ctx context.Context) ([]Build, error) {
	jobs, err := c.BatchV1().Jobs(BuildsNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	pods, err := c.CoreV1().Pods(BuildsNamespace).List(ctx, metav1.ListOptions{LabelSelector: "job-name"})
	if err != nil {
		return nil, err
	}
	out := []Build{}
	for _, j := range jobs.Items {
		out = append(out, buildOf(j, pods.Items))
	}
	sort.Slice(out, func(a, b int) bool { return out[a].StartedAt > out[b].StartedAt })
	return out, nil
}

func buildOf(j batchv1.Job, pods []corev1.Pod) Build {
	b := Build{Name: j.Name, State: "running", Image: buildImage(j)}
	if t := j.Status.StartTime; t != nil {
		b.StartedAt = t.UTC().Format(time.RFC3339)
	}
	for _, cond := range j.Status.Conditions {
		if cond.Status != corev1.ConditionTrue {
			continue
		}
		switch cond.Type {
		case batchv1.JobComplete:
			b.State = "succeeded"
		case batchv1.JobFailed:
			b.State = "failed"
		}
	}
	if t := j.Status.CompletionTime; t != nil {
		b.FinishedAt = t.UTC().Format(time.RFC3339)
	}
	var newest time.Time
	for _, p := range pods {
		if p.Labels["job-name"] == j.Name && p.CreationTimestamp.After(newest) {
			newest, b.Pod = p.CreationTimestamp.Time, p.Name
		}
	}
	return b
}

func buildImage(j batchv1.Job) string {
	for _, ctr := range j.Spec.Template.Spec.Containers {
		for _, arg := range append(append([]string{}, ctr.Command...), ctr.Args...) {
			for _, part := range strings.Split(arg, ",") {
				if name, ok := strings.CutPrefix(part, "name="); ok {
					return name
				}
			}
		}
	}
	return ""
}
