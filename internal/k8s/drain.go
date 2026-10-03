package k8s

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/kubectl/pkg/drain"
)

const CordonedBy = "kubit.dev/cordoned-by"

func (c *Client) Drain(ctx context.Context, name string, timeout time.Duration, log io.Writer) error {
	return c.drain(ctx, name, drainHelper(ctx, c.Interface, timeout, log, false))
}

func (c *Client) DrainAll(ctx context.Context, name string, timeout time.Duration, log io.Writer) error {
	return c.drain(ctx, name, drainHelper(ctx, c.Interface, timeout, log, true))
}

func drainHelper(ctx context.Context, client kubernetes.Interface, timeout time.Duration, log io.Writer, unmanaged bool) *drain.Helper {
	return &drain.Helper{
		Ctx:                 ctx,
		Client:              client,
		Force:               unmanaged,
		IgnoreAllDaemonSets: true,
		DeleteEmptyDirData:  true,
		GracePeriodSeconds:  -1,
		Timeout:             timeout,
		Out:                 log,
		ErrOut:              log,
	}
}

func (c *Client) drain(ctx context.Context, name string, h *drain.Helper) error {
	node, err := c.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if err := drain.RunCordonOrUncordon(h, node, true); err != nil {
		return fmt.Errorf("cordon: %w", err)
	}
	if err := drain.RunNodeDrain(h, name); err != nil {
		if blocked := c.blockingBudgets(ctx, name); len(blocked) > 0 {
			return fmt.Errorf("drain: %w; blocked by PodDisruptionBudget %s", err, strings.Join(blocked, ", "))
		}
		return fmt.Errorf("drain: %w", err)
	}
	return nil
}

func (c *Client) blockingBudgets(ctx context.Context, node string) []string {
	pods, err := c.CoreV1().Pods("").List(ctx, metav1.ListOptions{FieldSelector: "spec.nodeName=" + node})
	if err != nil {
		return nil
	}
	pdbs, err := c.PolicyV1().PodDisruptionBudgets("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil
	}
	return budgetsBlocking(pods.Items, pdbs.Items)
}

func budgetsBlocking(pods []corev1.Pod, pdbs []policyv1.PodDisruptionBudget) []string {
	var out []string
	for _, b := range pdbs {
		if b.Status.DisruptionsAllowed > 0 {
			continue
		}
		sel, err := metav1.LabelSelectorAsSelector(b.Spec.Selector)
		if err != nil || sel.Empty() {
			continue
		}
		for _, p := range pods {
			if p.Namespace == b.Namespace && p.DeletionTimestamp == nil && sel.Matches(labels.Set(p.Labels)) {
				out = append(out, fmt.Sprintf("%s/%s (pod %s)", b.Namespace, b.Name, p.Name))
				break
			}
		}
	}
	return out
}

func (c *Client) CordonState(ctx context.Context, name string) (unschedulable bool, by string, err error) {
	node, err := c.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return false, "", err
	}
	return node.Spec.Unschedulable, node.Annotations[CordonedBy], nil
}

func (c *Client) MarkCordon(ctx context.Context, name, by string) error {
	return c.annotateCordon(ctx, name, fmt.Sprintf("%q", by))
}

func (c *Client) annotateCordon(ctx context.Context, name, value string) error {
	patch := fmt.Sprintf(`{"metadata":{"annotations":{%q:%s}}}`, CordonedBy, value)
	_, err := c.CoreV1().Nodes().Patch(ctx, name, types.MergePatchType, []byte(patch), metav1.PatchOptions{})
	return err
}

func (c *Client) Uncordon(ctx context.Context, name string) error {
	node, err := c.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if err := drain.RunCordonOrUncordon(&drain.Helper{Ctx: ctx, Client: c.Interface}, node, false); err != nil {
		return err
	}
	if _, ok := node.Annotations[CordonedBy]; !ok {
		return nil
	}
	return c.annotateCordon(ctx, name, "null")
}

func (c *Client) NodeReady(ctx context.Context, name string) (ready, found bool, err error) {
	node, err := c.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return statusOf(node).Ready, true, nil
}

func (c *Client) DeleteNode(ctx context.Context, name string) error {
	return c.CoreV1().Nodes().Delete(ctx, name, metav1.DeleteOptions{})
}

func (c *Client) DeletePodsOnNode(ctx context.Context, node string) (int, error) {
	pods, err := c.CoreV1().Pods("").List(ctx, metav1.ListOptions{FieldSelector: "spec.nodeName=" + node})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, p := range pods.Items {
		if err := c.CoreV1().Pods(p.Namespace).Delete(ctx, p.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return n, err
		}
		n++
	}
	return n, nil
}
