package k8s

import (
	"context"
	"fmt"
	"io"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubectl/pkg/drain"
)

// Drain cordons the node and evicts its pods the way `kubectl drain
// --ignore-daemonsets --delete-emptydir-data` does.
func (c *Client) Drain(ctx context.Context, name string, timeout time.Duration, log io.Writer) error {
	node, err := c.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	h := &drain.Helper{
		Ctx:                 ctx,
		Client:              c.Clientset,
		IgnoreAllDaemonSets: true,
		DeleteEmptyDirData:  true,
		GracePeriodSeconds:  -1,
		Timeout:             timeout,
		Out:                 log,
		ErrOut:              log,
	}
	if err := drain.RunCordonOrUncordon(h, node, true); err != nil {
		return fmt.Errorf("cordon: %w", err)
	}
	if err := drain.RunNodeDrain(h, name); err != nil {
		return fmt.Errorf("drain: %w", err)
	}
	return nil
}

func (c *Client) Uncordon(ctx context.Context, name string) error {
	node, err := c.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	return drain.RunCordonOrUncordon(&drain.Helper{Ctx: ctx, Client: c.Clientset}, node, false)
}

func (c *Client) DeleteNode(ctx context.Context, name string) error {
	return c.CoreV1().Nodes().Delete(ctx, name, metav1.DeleteOptions{})
}
