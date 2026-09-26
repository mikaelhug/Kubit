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
	gen, err := config.Generate(c, bundle, m.installer(c))
	if err != nil {
		return err
	}
	nodes := orderedNodes(c)
	if wantKubelet == "" {
		sink.plan(nodeSteps(nodes, "Apply")...)
	}
	for _, n := range nodes {
		step := nodeStep(n)
		err := sink.run(step, func() error {
			if err := m.applyNodeConfig(ctx, n, gen.Nodes[n.Hostname], sec.Talosconfig, step, sink); err != nil {
				return err
			}
			if wantKubelet != "" {
				if err := waitKubeletVersion(ctx, kc, n.Hostname, wantKubelet, m.Timeouts.Ready); err != nil {
					return err
				}
			} else if err := kc.WaitReady(ctx, []string{n.Hostname}, m.Timeouts.Ready, nil); err != nil {
				return err
			}
			sink.emit(Info, step, n.Hostname, "Ready")
			return nil
		})
		if err != nil {
			return fmt.Errorf("%s: %w", n.Hostname, err)
		}
	}
	return nil
}

func (m *Manager) applyNodeConfig(ctx context.Context, n config.Node, cfg []byte, talosconfig []byte, step string, sink Sink) error {
	dial, cancel := context.WithTimeout(ctx, 30*time.Second)
	tc, err := talos.Dial(dial, n.IP, talosconfig)
	cancel()
	if err != nil {
		return err
	}
	details, err := tc.ApplyDryRun(ctx, cfg)
	if err != nil {
		tc.Close()
		return fmt.Errorf("dry run: %w", err)
	}
	bootID, _ := tc.BootID(ctx)
	err = tc.Apply(ctx, cfg)
	tc.Close()
	if err != nil {
		return fmt.Errorf("apply: %w", err)
	}
	if err := m.Store.PutNodeMachineConfig(ctx, n.IP, cfg); err != nil {
		return err
	}
	sink.emit(Info, step, n.Hostname, "applied: %s", summarizeDryRun(details))
	if wantReboot(details) {
		return talos.WaitForReboot(ctx, n.IP, talosconfig, bootID, m.Timeouts.Install)
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
