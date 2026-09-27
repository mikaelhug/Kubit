package cluster

import (
	"context"
	"time"

	"github.com/mikael/kubit/internal/talos"
)

const (
	rpcTimeout        = 30 * time.Second
	applyTimeout      = 2 * time.Minute
	readdressTimeout  = time.Minute
	rebootTimeout     = time.Minute
	resetTimeout      = 2 * time.Minute
	credentialTimeout = time.Minute
	manifestsTimeout  = time.Minute
	snapshotTimeout   = 10 * time.Minute
)

func readBootID(ctx context.Context, tc *talos.Client) (string, error) {
	call, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	return tc.BootID(call)
}

func applyConfig(ctx context.Context, tc *talos.Client, cfg []byte, timeout time.Duration) error {
	call, cancel := context.WithTimeout(ctx, timeout)
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
	return tc.RebootMachine(call)
}

func upgradeNode(ctx context.Context, tc *talos.Client, image string, timeout time.Duration) error {
	call, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return tc.UpgradeTo(call, image)
}

func resetNode(ctx context.Context, tc *talos.Client) error {
	call, cancel := context.WithTimeout(ctx, resetTimeout)
	defer cancel()
	return tc.ResetToMaintenance(call)
}

func resetEphemeral(ctx context.Context, tc *talos.Client) error {
	call, cancel := context.WithTimeout(ctx, resetTimeout)
	defer cancel()
	return tc.ResetEphemeral(call)
}

func restartService(ctx context.Context, tc *talos.Client, id string) error {
	call, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	return tc.RestartService(call, id)
}

func versionTag(ctx context.Context, tc *talos.Client) (string, error) {
	call, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	return tc.VersionTag(call)
}

func installedSchematic(ctx context.Context, tc *talos.Client) (string, error) {
	call, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	return tc.InstalledSchematic(call)
}

func generateTalosconfig(ctx context.Context, tc *talos.Client, ttl time.Duration) ([]byte, error) {
	call, cancel := context.WithTimeout(ctx, credentialTimeout)
	defer cancel()
	return tc.GenerateTalosconfig(call, ttl)
}

func adminKubeconfig(ctx context.Context, tc *talos.Client, timeout time.Duration) ([]byte, error) {
	call, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return tc.AdminKubeconfig(call)
}

func bootstrapManifests(ctx context.Context, tc *talos.Client) ([]map[string]any, error) {
	call, cancel := context.WithTimeout(ctx, manifestsTimeout)
	defer cancel()
	return tc.BootstrapManifests(call)
}

func (m *Manager) rebootingAction(ctx context.Context, ip string, talosconfig []byte, action func(*talos.Client) error) error {
	tc, err := talos.Dial(ctx, ip, talosconfig)
	if err != nil {
		return err
	}
	bootID, err := readBootID(ctx, tc)
	if err == nil {
		err = action(tc)
	}
	tc.Close()
	if err != nil {
		return err
	}
	return talos.WaitForReboot(ctx, ip, talosconfig, bootID, m.Timeouts.Install)
}
