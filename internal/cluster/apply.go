package cluster

import (
	"context"
	"fmt"
	"strings"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/talos"
)

func (m *Manager) ApplyConfigs(ctx context.Context, c *config.Cluster, wantKubelet string, sink Sink) error {
	name := c.Metadata.Name
	sec, bundle, err := m.loadSecrets(ctx, name)
	if err != nil {
		return err
	}
	kc, err := kubeClientOf(name, sec)
	if err != nil {
		return err
	}
	gen, err := config.Generate(c, bundle, m.installer(c))
	if err != nil {
		return err
	}
	nodes := orderedNodes(c)
	if wantKubelet == "" {
		sink.Plan(nodeSteps(nodes, "Apply")...)
	}
	for _, n := range nodes {
		step := nodeStep(n)
		err := sink.Run(step, func() error {
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
			sink.Emit(Info, step, n.Hostname, "Ready")
			return nil
		})
		if err != nil {
			return fmt.Errorf("%s: %w", n.Hostname, err)
		}
	}
	return nil
}

func (m *Manager) applyNodeConfig(ctx context.Context, n config.Node, cfg []byte, talosconfig []byte, step string, sink Sink) error {
	tc, err := talos.Dial(ctx, n.IP, talosconfig)
	if err != nil {
		return err
	}
	details, err := applyDryRun(ctx, tc, cfg)
	if err != nil {
		tc.Close()
		return fmt.Errorf("dry run: %w", err)
	}
	bootID, _ := readBootID(ctx, tc)
	err = applyConfig(ctx, tc, cfg)
	tc.Close()
	if err != nil {
		return fmt.Errorf("apply: %w", err)
	}
	if err := m.Store.PutNodeMachineConfig(ctx, n.IP, cfg, config.HasSystemVolume(cfg)); err != nil {
		return err
	}
	sink.Emit(Info, step, n.Hostname, "applied: %s", summarizeDryRun(details))
	if wantReboot(details) {
		return talos.WaitForReboot(ctx, n.IP, talosconfig, bootID, m.Timeouts.Install)
	}
	return nil
}

func summarizeDryRun(details string) string {
	for _, line := range strings.Split(details, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(strings.ToLower(line), "reboot") {
			return line
		}
	}
	return strings.TrimSpace(strings.SplitN(details, "\n", 2)[0])
}

func wantReboot(details string) bool {
	return details != "" && !strings.Contains(strings.ToLower(details), "without a reboot")
}
