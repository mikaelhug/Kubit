package k8s

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Workload struct {
	Kind      string `json:"kind"`
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

func (c *Client) Workloads(ctx context.Context) ([]Workload, error) {
	var out []Workload
	deps, err := c.deployments(ctx, "")
	if err != nil {
		return nil, err
	}
	for _, d := range deps {
		out = append(out, aged(Workload{Kind: "Deployment", Namespace: d.Namespace, Name: d.Name, Ready: d.Status.ReadyReplicas, Desired: *d.Spec.Replicas, Available: d.Status.AvailableReplicas == *d.Spec.Replicas, Images: images(d.Spec.Template.Spec), Selector: metav1.FormatLabelSelector(d.Spec.Selector)}, d.CreationTimestamp))
	}
	dss, err := c.daemonSets(ctx, "")
	if err != nil {
		return nil, err
	}
	for _, d := range dss {
		out = append(out, aged(Workload{Kind: "DaemonSet", Namespace: d.Namespace, Name: d.Name, Ready: d.Status.NumberReady, Desired: d.Status.DesiredNumberScheduled, Available: d.Status.NumberReady == d.Status.DesiredNumberScheduled, Images: images(d.Spec.Template.Spec), Selector: metav1.FormatLabelSelector(d.Spec.Selector)}, d.CreationTimestamp))
	}
	sts, err := c.statefulSets(ctx, "")
	if err != nil {
		return nil, err
	}
	for _, d := range sts {
		out = append(out, aged(Workload{Kind: "StatefulSet", Namespace: d.Namespace, Name: d.Name, Ready: d.Status.ReadyReplicas, Desired: *d.Spec.Replicas, Available: d.Status.ReadyReplicas == *d.Spec.Replicas, Images: images(d.Spec.Template.Spec), Selector: metav1.FormatLabelSelector(d.Spec.Selector)}, d.CreationTimestamp))
	}
	jobs, err := c.jobs(ctx, "")
	if err != nil {
		return nil, err
	}
	for _, j := range jobs {
		out = append(out, aged(Workload{Kind: "Job", Namespace: j.Namespace, Name: j.Name, Ready: j.Status.Succeeded, Desired: 1, Available: j.Status.Succeeded > 0, Images: images(j.Spec.Template.Spec)}, j.CreationTimestamp))
	}
	crons, err := c.cronJobs(ctx)
	if err != nil {
		return nil, err
	}
	for _, j := range crons {
		out = append(out, cronWorkload(j))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func cronWorkload(j batchv1.CronJob) Workload {
	return aged(Workload{Kind: "CronJob", Namespace: j.Namespace, Name: j.Name, Ready: int32(len(j.Status.Active)), Available: true, Images: images(j.Spec.JobTemplate.Spec.Template.Spec)}, j.CreationTimestamp)
}

func aged(w Workload, created metav1.Time) Workload {
	w.Age, w.AgeSec = age(created)
	return w
}

func age(created metav1.Time) (string, int64) {
	d := metav1.Now().Sub(created.Time)
	return d.Truncate(time.Second).String(), int64(d.Seconds())
}

type Namespace struct {
	Name     string `json:"name"`
	Phase    string `json:"phase"`
	Security string `json:"security,omitempty"`
	AgeSec   int64  `json:"ageSec"`
}

func (c *Client) Namespaces(ctx context.Context) ([]Namespace, error) {
	list, err := c.namespaces(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Namespace, 0, len(list))
	for _, n := range list {
		out = append(out, namespaceOf(n))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func namespaceOf(n corev1.Namespace) Namespace {
	ns := Namespace{Name: n.Name, Phase: string(n.Status.Phase), Security: n.Labels["pod-security.kubernetes.io/enforce"]}
	_, ns.AgeSec = age(n.CreationTimestamp)
	return ns
}

func images(spec corev1.PodSpec) string {
	var imgs []string
	for _, ctr := range spec.Containers {
		imgs = append(imgs, ctr.Image)
	}
	return strings.Join(imgs, ", ")
}

func (c *Client) Pods(ctx context.Context, namespace, selector string) ([]PodSummary, error) {
	out, err := c.PodSummaries(ctx, namespace, selector)
	if err != nil {
		return nil, err
	}
	usage, _ := c.podUsages(ctx)
	for i, ps := range out {
		if u, ok := usage[ps.Namespace+"/"+ps.Name]; ok {
			out[i].UsageCPU, out[i].UsageMem = u.CPUMilli, u.MemoryBytes
		}
	}
	return out, nil
}

func (c *Client) PodSummaries(ctx context.Context, namespace, selector string) ([]PodSummary, error) {
	pods, err := c.pods(ctx, namespace, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, err
	}
	out := make([]PodSummary, 0, len(pods))
	for _, p := range pods {
		out = append(out, podSummary(&p))
	}
	return out, nil
}

func podSummary(p *corev1.Pod) PodSummary {
	ps := PodSummary{Namespace: p.Namespace, Name: p.Name, Phase: string(p.Status.Phase), Node: p.Spec.NodeName}
	ps.Age, ps.AgeSec = age(p.CreationTimestamp)
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

type PodEvent struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
	Since   string `json:"since,omitempty"`
}

func (c *Client) PodEvents(ctx context.Context, namespace, name string) ([]PodEvent, error) {
	list, err := c.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{FieldSelector: "involvedObject.name=" + name + ",involvedObject.kind=Pod"})
	if err != nil {
		return nil, err
	}
	out := []PodEvent{}
	for _, e := range list.Items {
		t := e.LastTimestamp
		if t.IsZero() {
			t = e.CreationTimestamp
		}
		out = append(out, PodEvent{Type: e.Type, Status: fmt.Sprint(e.Count), Reason: e.Reason, Message: e.Message, Since: t.UTC().Format("2006-01-02T15:04:05Z")})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Since > out[j].Since })
	return out, nil
}

func (c *Client) PodLogs(ctx context.Context, namespace, name, container string, tail int64, follow bool) (io.ReadCloser, error) {
	opts := &corev1.PodLogOptions{Container: container, Follow: follow, TailLines: &tail}
	if !follow {
		return c.CoreV1().Pods(namespace).GetLogs(name, opts).Stream(ctx)
	}
	cs, err := c.streamClient()
	if err != nil {
		return nil, err
	}
	return cs.CoreV1().Pods(namespace).GetLogs(name, opts).Stream(ctx)
}

func (c *Client) PodCount(ctx context.Context) (map[string]int, error) {
	pods, err := c.pods(ctx, "", metav1.ListOptions{FieldSelector: "status.phase=Running"})
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, p := range pods {
		out[p.Spec.NodeName]++
	}
	return out, nil
}
