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
	kc, err := m.KubeClientFor(name, sec)
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
			before := kubeBootID(ctx, kc, n.Hostname)
			rebooted, err := m.applyNodeConfig(ctx, n, gen.Nodes[n.Hostname], sec.Talosconfig, step, sink)
			if err != nil {
				return err
			}
			switch {
			case rebooted:
				if err := m.waitBack(ctx, c, kc, n, before, sec.Talosconfig); err != nil {
					return err
				}
			case wantKubelet == "":
				if err := kc.WaitReady(ctx, []string{n.Hostname}, m.Timeouts.Ready, nil); err != nil {
					return err
				}
			}
			if wantKubelet != "" {
				if err := waitKubeletVersion(ctx, kc, n.Hostname, wantKubelet, m.Timeouts.Ready); err != nil {
					return err
				}
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

func (m *Manager) applyNodeConfig(ctx context.Context, n config.Node, cfg []byte, talosconfig []byte, step string, sink Sink) (rebooted bool, err error) {
	tc, err := talos.Dial(ctx, n.IP, talosconfig)
	if err != nil {
		return false, err
	}
	details, err := applyDryRun(ctx, tc, cfg)
	if err != nil {
		tc.Close()
		return false, fmt.Errorf("dry run: %w", err)
	}
	bootID, err := readBootID(ctx, tc)
	if err != nil {
		tc.Close()
		return false, fmt.Errorf("boot id: %w", err)
	}
	err = applyConfig(ctx, tc, cfg, applyTimeout)
	tc.Close()
	if err != nil {
		return false, fmt.Errorf("apply: %w", err)
	}
	if err := m.Store.PutNodeMachineConfig(ctx, n.IP, cfg, config.HasSystemVolume(cfg)); err != nil {
		return false, err
	}
	sink.Emit(Info, step, n.Hostname, "applied: %s", summarizeDryRun(details))
	if !wantReboot(details) {
		return false, nil
	}
	return true, talos.WaitForReboot(ctx, n.IP, talosconfig, bootID, m.Timeouts.Install)
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
