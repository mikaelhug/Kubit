package cluster

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/talos"
)

// ApplyConfigs regenerates every node's machine config from cluster.yaml with the stored
// secrets and applies it, control planes first, waiting for each node to be Ready (and
// on the expected kubelet version when wantKubelet is set) before moving on. Talos
// applies without a reboot whenever it can and reboots otherwise.
func (m *Manager) ApplyConfigs(ctx context.Context, c *config.Cluster, wantKubelet string, sink Sink) error {
	name := c.Metadata.Name
	sec, bundle, err := m.loadSecrets(ctx, name)
	if err != nil {
		return err
	}
	kc, err := m.KubeClient(ctx, name)
	if err != nil {
		return err
	}
	gen, err := config.Generate(c, bundle, m.installerImage(c))
	if err != nil {
		return err
	}
	for _, n := range orderedNodes(c) {
		cfg := gen.Nodes[n.Hostname]
		dial, cancel := context.WithTimeout(ctx, 30*time.Second)
		tc, err := talos.Dial(dial, n.IP, sec.Talosconfig)
		cancel()
		if err != nil {
			return fmt.Errorf("%s: %w", n.Hostname, err)
		}
		details, err := tc.ApplyDryRun(ctx, cfg)
		if err != nil {
			tc.Close()
			return fmt.Errorf("%s: dry run: %w", n.Hostname, err)
		}
		bootID, _ := tc.BootID(ctx)
		err = tc.Apply(ctx, cfg)
		tc.Close()
		if err != nil {
			return fmt.Errorf("%s: apply: %w", n.Hostname, err)
		}
		if err := m.Store.PutNodeMachineConfig(ctx, n.IP, cfg); err != nil {
			return err
		}
		sink.emit(Info, "apply", n.Hostname, "applied: %s", summarizeDryRun(details))
		if wantReboot(details) {
			if err := talos.WaitForReboot(ctx, n.IP, sec.Talosconfig, bootID, m.Timeouts.Install); err != nil {
				return fmt.Errorf("%s: %w", n.Hostname, err)
			}
		}
		if wantKubelet != "" {
			if err := waitKubeletVersion(ctx, kc, n.Hostname, wantKubelet, m.Timeouts.Ready); err != nil {
				return fmt.Errorf("%s: %w", n.Hostname, err)
			}
		} else if err := kc.WaitReady(ctx, []string{n.Hostname}, m.Timeouts.Ready, nil); err != nil {
			return fmt.Errorf("%s: %w", n.Hostname, err)
		}
		sink.emit(Info, "apply", n.Hostname, "Ready")
	}
	return nil
}

// summarizeDryRun keeps the line that states the reboot decision out of Talos' multi-line
// dry-run report.
func summarizeDryRun(details string) string {
	for _, line := range strings.Split(details, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(strings.ToLower(line), "reboot") {
			return line
		}
	}
	return strings.TrimSpace(strings.SplitN(details, "\n", 2)[0])
}

// wantReboot reads Talos' dry-run mode details ("Applied configuration with a reboot"
// vs "... without a reboot").
func wantReboot(details string) bool {
	return details != "" && !strings.Contains(strings.ToLower(details), "without a reboot")
}
