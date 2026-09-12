package talos

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/cosi-project/runtime/pkg/safe"
	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/resources/k8s"
	"github.com/siderolabs/talos/pkg/machinery/resources/runtime"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

// Apply pushes a machine config; mode AUTO reboots only when the change needs it, which
// on a maintenance node means install-and-reboot.
func (c *Client) Apply(ctx context.Context, cfg []byte) error {
	_, err := c.ApplyConfiguration(c.Context(ctx), &machineapi.ApplyConfigurationRequest{
		Data: cfg,
		Mode: machineapi.ApplyConfigurationRequest_AUTO,
	})
	return err
}

// ApplyDryRun validates the config on the node and reports whether applying would reboot.
func (c *Client) ApplyDryRun(ctx context.Context, cfg []byte) (string, error) {
	resp, err := c.ApplyConfiguration(c.Context(ctx), &machineapi.ApplyConfigurationRequest{
		Data: cfg, Mode: machineapi.ApplyConfigurationRequest_AUTO, DryRun: true,
	})
	if err != nil {
		return "", err
	}
	if len(resp.Messages) == 0 {
		return "", nil
	}
	return resp.Messages[0].ModeDetails, nil
}

// BootstrapEtcd initialises the etcd cluster on this control plane; call exactly once
// per cluster. Talos answers AlreadyExists-style errors once bootstrapped.
func (c *Client) BootstrapEtcd(ctx context.Context) error {
	return c.Bootstrap(c.Context(ctx), &machineapi.BootstrapRequest{})
}

// ServiceHealthy reports whether a Talos service is running and passing health checks.
func (c *Client) ServiceHealthy(ctx context.Context, id string) (bool, error) {
	infos, err := c.ServiceInfo(c.Context(ctx), id)
	if err != nil {
		return false, err
	}
	for _, i := range infos {
		if i.Service == nil {
			continue
		}
		if i.Service.State == "Running" && i.Service.Health != nil && i.Service.Health.Healthy {
			return true, nil
		}
	}
	return false, nil
}

// EtcdMemberCount returns how many members the etcd cluster currently has.
func (c *Client) EtcdMemberCount(ctx context.Context) (int, error) {
	resp, err := c.EtcdMemberList(c.Context(ctx), &machineapi.EtcdMemberListRequest{})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, m := range resp.Messages {
		n += len(m.Members)
	}
	return n, nil
}

// BootID identifies the current boot by the kernel's boot time; it changes on every
// reboot and, unlike the file API, SystemStat is served in maintenance mode too.
func (c *Client) BootID(ctx context.Context) (string, error) {
	resp, err := c.MachineClient.SystemStat(c.Context(ctx), &emptypb.Empty{})
	if err != nil {
		return "", err
	}
	if len(resp.Messages) == 0 {
		return "", errors.New("empty SystemStat response")
	}
	return strconv.FormatUint(resp.Messages[0].BootTime, 10), nil
}

// WaitForReboot polls until the node answers over mTLS from a different boot than
// prevBootID, i.e. it has rebooted into the installed system. Right after an apply in
// maintenance mode apid already accepts cluster credentials and reports stage "booting"
// while the installer still runs, so neither a Version answer nor the stage alone is
// proof of installation.
func WaitForReboot(ctx context.Context, ip string, talosconfig []byte, prevBootID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if PortOpen(ip, 2*time.Second) {
			attempt, cancel := context.WithTimeout(ctx, 10*time.Second)
			id, err := bootIDWith(attempt, ip, talosconfig)
			cancel()
			switch {
			case err == nil && id != prevBootID:
				return nil
			case err == nil:
				last = NotReady("still on the pre-install boot")
			default:
				last = err
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	return fmt.Errorf("%s: not back after reboot within %s: %w", ip, timeout, last)
}

func bootIDWith(ctx context.Context, ip string, talosconfig []byte) (string, error) {
	c, err := Dial(ctx, ip, talosconfig)
	if err != nil {
		return "", err
	}
	defer c.Close()
	return c.BootID(ctx)
}

// Stage returns the node's runtime.MachineStatus stage over mTLS.
func Stage(ctx context.Context, ip string, talosconfig []byte) (string, error) {
	c, err := Dial(ctx, ip, talosconfig)
	if err != nil {
		return "", err
	}
	defer c.Close()
	st, err := safe.StateGetByID[*runtime.MachineStatus](c.Context(ctx), c.COSI, runtime.MachineStatusID)
	if err != nil {
		return "", err
	}
	return st.TypedSpec().Stage.String(), nil
}

// Retry runs f until it succeeds, the context ends, or the timeout passes. Retryable
// failures are connectivity and readiness errors; anything else stops immediately.
func Retry(ctx context.Context, timeout, interval time.Duration, f func() error) error {
	deadline := time.Now().Add(timeout)
	for {
		err := f()
		if err == nil {
			return nil
		}
		if !retryable(err) || time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// NotReady marks a condition that is expected to clear with time (etcd still joining,
// kubelet not registered) as opposed to a failure.
type NotReady string

func (e NotReady) Error() string { return string(e) }

func retryable(err error) bool {
	var nr NotReady
	if errors.As(err, &nr) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if _, isGRPC := status.FromError(err); !isGRPC {
		return false
	}
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded, codes.FailedPrecondition, codes.Aborted, codes.Unknown:
		return true
	}
	return false
}

// BootstrapManifests returns the Kubernetes objects Talos renders for the control plane
// (kube-proxy, CoreDNS, flannel, RBAC, ...). Talos applies them once at bootstrap;
// after a Kubernetes upgrade they must be re-applied, which is what
// `talosctl upgrade-k8s` does in its manifest sync step.
func (c *Client) BootstrapManifests(ctx context.Context) ([]map[string]any, error) {
	list, err := safe.StateListAll[*k8s.Manifest](c.Context(ctx), c.COSI)
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for m := range list.All() {
		for _, item := range m.TypedSpec().Items {
			out = append(out, item.Object)
		}
	}
	return out, nil
}
