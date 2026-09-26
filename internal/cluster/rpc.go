package cluster

import (
	"context"
	"time"

	"github.com/mikael/kubit/internal/talos"
)

const (
	rpcTimeout     = 30 * time.Second
	applyTimeout   = 2 * time.Minute
	rebootTimeout  = time.Minute
	resetTimeout   = 2 * time.Minute
	upgradeTimeout = 5 * time.Minute
)

func readBootID(ctx context.Context, tc *talos.Client) (string, error) {
	call, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	return tc.BootID(call)
}

func applyConfig(ctx context.Context, tc *talos.Client, cfg []byte) error {
	call, cancel := context.WithTimeout(ctx, applyTimeout)
	defer cancel()
	return tc.Apply(call, cfg)
}

func applyDryRun(ctx context.Context, tc *talos.Client, cfg []byte) (string, error) {
	call, cancel := context.WithTimeout(ctx, applyTimeout)
	defer cancel()
	return tc.ApplyDryRun(call, cfg)
}

func rebootNode(ctx context.Context, tc *talos.Client) error {
	call, cancel := context.WithTimeout(ctx, rebootTimeout)
	defer cancel()
	return tc.Reboot(tc.Context(call))
}

func upgradeNode(ctx context.Context, tc *talos.Client, image string) error {
	call, cancel := context.WithTimeout(ctx, upgradeTimeout)
	defer cancel()
	_, err := tc.Upgrade(tc.Context(call), image, false, false)
	return err
}

func resetNode(ctx context.Context, tc *talos.Client) error {
	call, cancel := context.WithTimeout(ctx, resetTimeout)
	defer cancel()
	return tc.Reset(tc.Context(call), true, true)
}

func resetEphemeral(ctx context.Context, tc *talos.Client) error {
	call, cancel := context.WithTimeout(ctx, resetTimeout)
	defer cancel()
	return tc.ResetEphemeral(call)
}
