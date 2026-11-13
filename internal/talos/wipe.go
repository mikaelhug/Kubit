package talos

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cosi-project/runtime/pkg/safe"
	storageapi "github.com/siderolabs/talos/pkg/machinery/api/storage"
	"github.com/siderolabs/talos/pkg/machinery/resources/block"
)

func (c *Client) WipeDisk(ctx context.Context, devPath string) error {
	return c.BlockDeviceWipe(ctx, &storageapi.BlockDeviceWipeRequest{Devices: []*storageapi.BlockDeviceWipeDescriptor{{
		Device: strings.TrimPrefix(devPath, "/dev/"),
		Method: storageapi.BlockDeviceWipeDescriptor_FAST,
	}}})
}

func (c *Client) WaitUserVolume(ctx context.Context, name string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	last := "not created"
	for {
		v, err := safe.StateGetByID[*block.VolumeStatus](ctx, c.COSI, "u-"+name)
		switch {
		case err == nil && v.TypedSpec().Phase == block.VolumePhaseReady:
			return nil
		case err == nil:
			last = v.TypedSpec().Phase.String()
			if msg := v.TypedSpec().ErrorMessage; msg != "" {
				last += ": " + msg
			}
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("volume %s is %s", name, last)
			}
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}
