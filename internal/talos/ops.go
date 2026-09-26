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
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/emptypb"
)

func (c *Client) Apply(ctx context.Context, cfg []byte) error {
	_, err := c.ApplyConfiguration(c.Context(ctx), &machineapi.ApplyConfigurationRequest{
		Data: cfg,
		Mode: machineapi.ApplyConfigurationRequest_AUTO,
	})
	return err
}

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

func (c *Client) BootstrapEtcd(ctx context.Context) error {
	return c.Bootstrap(c.Context(ctx), &machineapi.BootstrapRequest{})
}

func (c *Client) RestartService(ctx context.Context, id string) error {
	_, err := c.ServiceRestart(c.Context(ctx), id)
	return err
}

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

func WaitForReboot(ctx context.Context, ip string, talosconfig []byte, prevBootID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	sawCreds := false
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if PortOpen(ctx, ip, 2*time.Second) {
			attempt, cancel := context.WithTimeout(ctx, 10*time.Second)
			id, err := bootIDWith(attempt, ip, talosconfig)
			cancel()
			switch {
			case err == nil && id != prevBootID:
				return nil
			case err == nil:
				sawCreds = true
				last = NotReady("still on the pre-install boot")
			default:
				last = err
				if sawCreds {
					if r := Probe(ctx, ip, 2*time.Second); r.Err == nil && r.State == StateMaintenance {
						return fmt.Errorf("%s rebooted into maintenance mode, not the installed system — the disk-boot switch did not take", ip)
					}
				}
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

func (c *Client) GenerateTalosconfig(ctx context.Context, ttl time.Duration) ([]byte, error) {
	resp, err := c.GenerateClientConfiguration(c.Context(ctx), &machineapi.GenerateClientConfigurationRequest{Roles: []string{"os:admin"}, CrtTtl: durationpb.New(ttl)})
	if err != nil {
		return nil, err
	}
	if len(resp.Messages) == 0 || len(resp.Messages[0].Talosconfig) == 0 {
		return nil, errors.New("empty GenerateClientConfiguration response")
	}
	return resp.Messages[0].Talosconfig, nil
}

func (c *Client) VarAvailable(ctx context.Context) (avail, size uint64, err error) {
	resp, err := c.Mounts(c.Context(ctx))
	if err != nil {
		return 0, 0, err
	}
	for _, msg := range resp.Messages {
		for _, st := range msg.Stats {
			if st.MountedOn == "/var" {
				return st.Available, st.Size, nil
			}
		}
	}
	return 0, 0, errors.New("/var not found in mounts")
}
