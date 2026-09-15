package k8s

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Workload struct {
	Kind      string `json:"kind"` // Deployment | DaemonSet | StatefulSet | Job | CronJob
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Ready     int32  `json:"ready"`
	Desired   int32  `json:"desired"`
	Available bool   `json:"available"`
	Images    string `json:"images"`
	Age       string `json:"age"`
	AgeSec    int64  `json:"ageSec"`
	Selector  string `json:"selector,omitempty"`
}

// Workloads lists the controller objects across all namespaces.
func (c *Client) Workloads(ctx context.Context) ([]Workload, error) {
	var out []Workload
	age := func(t metav1.Time) string { return metav1.Now().Sub(t.Time).Truncate(1e9).String() }
	secs := func(t metav1.Time) int64 { return int64(metav1.Now().Sub(t.Time).Seconds()) }
	deps, err := c.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for _, d := range deps.Items {
		out = append(out, Workload{Kind: "Deployment", Namespace: d.Namespace, Name: d.Name, Ready: d.Status.ReadyReplicas, Desired: *d.Spec.Replicas, Available: d.Status.AvailableReplicas == *d.Spec.Replicas, Images: images(d.Spec.Template.Spec), Age: age(d.CreationTimestamp), AgeSec: secs(d.CreationTimestamp), Selector: metav1.FormatLabelSelector(d.Spec.Selector)})
	}
	dss, err := c.AppsV1().DaemonSets("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for _, d := range dss.Items {
		out = append(out, Workload{Kind: "DaemonSet", Namespace: d.Namespace, Name: d.Name, Ready: d.Status.NumberReady, Desired: d.Status.DesiredNumberScheduled, Available: d.Status.NumberReady == d.Status.DesiredNumberScheduled, Images: images(d.Spec.Template.Spec), Age: age(d.CreationTimestamp), AgeSec: secs(d.CreationTimestamp), Selector: metav1.FormatLabelSelector(d.Spec.Selector)})
	}
	sts, err := c.AppsV1().StatefulSets("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for _, d := range sts.Items {
		out = append(out, Workload{Kind: "StatefulSet", Namespace: d.Namespace, Name: d.Name, Ready: d.Status.ReadyReplicas, Desired: *d.Spec.Replicas, Available: d.Status.ReadyReplicas == *d.Spec.Replicas, Images: images(d.Spec.Template.Spec), Age: age(d.CreationTimestamp), AgeSec: secs(d.CreationTimestamp), Selector: metav1.FormatLabelSelector(d.Spec.Selector)})
	}
	jobs, err := c.BatchV1().Jobs("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for _, j := range jobs.Items {
		out = append(out, Workload{Kind: "Job", Namespace: j.Namespace, Name: j.Name, Ready: j.Status.Succeeded, Desired: 1, Available: j.Status.Succeeded > 0, Images: images(j.Spec.Template.Spec), Age: age(j.CreationTimestamp), AgeSec: secs(j.CreationTimestamp)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func images(spec corev1.PodSpec) string {
	var imgs []string
	for _, ctr := range spec.Containers {
		imgs = append(imgs, ctr.Image)
	}
	return strings.Join(imgs, ", ")
}

// Pods lists pods, optionally filtered by namespace and a label selector.
func (c *Client) Pods(ctx context.Context, namespace, selector string) ([]PodSummary, error) {
	pods, err := c.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, err
	}
	usage, _ := c.podUsages(ctx)
	out := make([]PodSummary, 0, len(pods.Items))
	for _, p := range pods.Items {
		ps := podSummary(&p)
		if u, ok := usage[p.Namespace+"/"+p.Name]; ok {
			ps.UsageCPU, ps.UsageMem = u.CPUMilli, u.MemoryBytes
		}
		out = append(out, ps)
	}
	return out, nil
}

func podSummary(p *corev1.Pod) PodSummary {
	ps := PodSummary{Namespace: p.Namespace, Name: p.Name, Phase: string(p.Status.Phase), Node: p.Spec.NodeName, Age: metav1.Now().Sub(p.CreationTimestamp.Time).Truncate(1e9).String(), AgeSec: int64(metav1.Now().Sub(p.CreationTimestamp.Time).Seconds())}
	ready := 0
	for _, cs := range p.Status.ContainerStatuses {
		if cs.Ready {
			ready++
		}
		ps.Restarts += cs.RestartCount
		if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
			ps.Phase = cs.State.Waiting.Reason
		}
	}
	ps.Ready = fmt.Sprintf("%d/%d", ready, len(p.Spec.Containers))
	for _, o := range p.OwnerReferences {
		ps.Owner = o.Kind
	}
	for _, ctr := range p.Spec.Containers {
		ps.Containers = append(ps.Containers, ctr.Name)
		ps.CPUMilli += ctr.Resources.Requests.Cpu().MilliValue()
		ps.MemBytes += ctr.Resources.Requests.Memory().Value()
	}
	return ps
}

// PodEvents returns Kubernetes events for one pod, newest first.
func (c *Client) PodEvents(ctx context.Context, namespace, name string) ([]Condition, error) {
	list, err := c.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{FieldSelector: "involvedObject.name=" + name + ",involvedObject.kind=Pod"})
	if err != nil {
		return nil, err
	}
	out := []Condition{}
	for _, e := range list.Items {
		t := e.LastTimestamp
		if t.IsZero() {
			t = e.CreationTimestamp
		}
		out = append(out, Condition{Type: e.Type, Status: fmt.Sprint(e.Count), Reason: e.Reason, Message: e.Message, Since: t.UTC().Format("2006-01-02T15:04:05Z")})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Since > out[j].Since })
	return out, nil
}

// PodLogs streams a container's log (follow keeps it open).
func (c *Client) PodLogs(ctx context.Context, namespace, name, container string, tail int64, follow bool) (io.ReadCloser, error) {
	opts := &corev1.PodLogOptions{Container: container, Follow: follow, TailLines: &tail}
	return c.CoreV1().Pods(namespace).GetLogs(name, opts).Stream(ctx)
}
