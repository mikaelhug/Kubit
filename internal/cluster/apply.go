package cluster

import (
	"context"
	"fmt"
	"strings"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/k8s"
	"github.com/mikael/kubit/internal/talos"
)

func (m *Manager) ApplyConfigs(ctx context.Context, c *config.Cluster, wantKubelet string, sink Sink) error {
	sec, gen, err := m.generateNodeConfigs(c)
	if err != nil {
		return err
	}
	kc, err := m.KubeClientFor(c.Metadata.Name, m.liveKubeconfig(sec))
	if err != nil {
		return err
	}
	nodes := orderedNodes(declared(c))
	if wantKubelet == "" {
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
				if err := kc.WaitReady(ctx, []string{n.Hostname}, readyTimeout, nil); err != nil {
					return err
				}
			}
			if wantKubelet != "" {
				if err := waitKubeletVersion(ctx, kc, n.Hostname, wantKubelet, readyTimeout); err != nil {
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
	if wantKubelet != "" {
		return nil
	}
	return m.syncManifestsStep(ctx, c, kc, sink)
}

func (m *Manager) syncManifestsStep(ctx context.Context, c *config.Cluster, kc *k8s.Client, sink Sink) error {
	return sink.Run(manifestsStep, func() error {
		if _, err := kc.Nodes(ctx); err != nil {
			return fmt.Errorf("Kubernetes API unreachable; bootstrap manifests not synced: %w", err)
		}
		return m.SyncManifests(ctx, c, sink)
	})
}

func (m *Manager) generateNodeConfigs(c *config.Cluster) (*Desired, *config.Generated, error) {
	sec, bundle, err := m.loadSecrets(c.Metadata.Name)
	if err != nil {
		return nil, nil, err
	}
	gen, err := m.machineConfigs(c, bundle)
	if err != nil {
		return nil, nil, err
	}
	return sec, gen, nil
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
	if configDiff(details) == "" {
		tc.Close()
		sink.Emit(Info, step, n.Hostname, "unchanged")
		return false, nil
	}
	bootID, err := readBootID(ctx, tc)
	if err != nil {
		tc.Close()
		return false, fmt.Errorf("boot id: %w", err)
	}
	err = applyConfig(ctx, tc, cfg)
	tc.Close()
	if err != nil {
		return false, fmt.Errorf("apply: %w", err)
	}
	sink.Emit(Info, step, n.Hostname, "applied: %s", summarizeDryRun(details))
	if !wantReboot(details) {
		return false, nil
	}
	return true, talos.WaitForReboot(ctx, n.IP, talosconfig, bootID, installTimeout)
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
