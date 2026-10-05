package cluster

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/tofu"
)

func (m *Manager) ClusterDir(name string) string {
	return filepath.Join(m.Home, "clusters", name)
}

func (m *Manager) platformDir(name string) string {
	return filepath.Join(m.ClusterDir(name), "infra", "platform")
}

func (m *Manager) tofuBin(ctx context.Context) (string, error) {
	return tofu.Binary(ctx, filepath.Join(m.Home, "bin"))
}

func (m *Manager) writeCredentials(ctx context.Context, name string) (kubeconfigPath string, err error) {
	sec, err := m.Store.GetClusterSecrets(ctx, name)
	if err != nil {
		return "", err
	}
	if sec.Kubeconfig == nil {
		return "", fmt.Errorf("cluster %s has no kubeconfig yet", name)
	}
	dir := m.ClusterDir(name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	kubeconfigPath = filepath.Join(dir, "kubeconfig")
	if err := os.WriteFile(kubeconfigPath, sec.Kubeconfig, 0o600); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "talosconfig"), sec.Talosconfig, 0o600); err != nil {
		return "", err
	}
	return kubeconfigPath, nil
}

type platformRun struct {
	*tofu.Runner
	cluster  *config.Cluster
	specHash string
}

func specHash(spec []byte) string {
	sum := sha256.Sum256(spec)
	return hex.EncodeToString(sum[:])
}

func (m *Manager) platformRunner(ctx context.Context, name string, sink Sink) (*platformRun, error) {
	sink.Plan(platformSteps...)
	sink.Begin("render")
	c, row, err := m.LoadCluster(ctx, name)
	if err != nil {
		return nil, err
	}
	if row.State != StateBootstrapped && row.State != StateReady {
		return nil, fmt.Errorf("cluster %s is %s; the platform needs a bootstrapped cluster", name, row.State)
	}
	if c.Spec.Platform.Longhorn.Enabled {
		if id, pools, err := m.desiredSchematics(ctx, c); err != nil {
			sink.Emit(Warn, "render", "", "could not check the node image for Longhorn's extensions: %v", err)
		} else if imageOutdated(c, id, pools) {
			return nil, fmt.Errorf("Longhorn needs the Talos extensions %s on every node first: upgrade Talos (Lifecycle), then plan again", strings.Join(config.LonghornExtensions, " and "))
		}
	}
	if c.Spec.Platform.Builds.Enabled {
		if host := m.registryMirrorMissing(ctx, c); host != "" {
			return nil, fmt.Errorf("Builds needs the registry mirror in every node config (%s has none): apply node configs (Settings), then plan again", host)
		}
	}
	kubeconfig, err := m.writeCredentials(ctx, name)
	if err != nil {
		return nil, err
	}
	dir := m.platformDir(name)
	var ingressIP string
	if p, err := m.Store.GetPlatformStatus(ctx, name); err == nil {
		ingressIP = p.Outputs["ingress_ip"]
	}
	if err := tofu.Render(dir, c, kubeconfig, ingressIP); err != nil {
		return nil, err
	}
	bin, err := m.tofuBin(ctx)
	if err != nil {
		return nil, err
	}
	sink.Emit(Info, "render", "", "rendered %s (tofu %s)", dir, bin)
	if c.Spec.Platform.Flux.Enabled {
		k, err := m.SOPSKey(ctx, name)
		if err != nil {
			return nil, err
		}
		sink.Emit(Info, "render", "", "SOPS key %s goes to %s/%s on apply", k.Recipient, tofu.SOPSNamespace, tofu.SOPSSecret)
	}
	sink.End("render")
	r := &tofu.Runner{Bin: bin, Dir: dir, Log: tofuLogger(sink)}
	if err := sink.Run("init", func() error { return r.Init(ctx) }); err != nil {
		return nil, err
	}
	return &platformRun{Runner: r, cluster: c, specHash: specHash(row.Spec)}, nil
}

var platformSteps = Steps(
	"render", "Render infra/platform from cluster.yaml",
	"init", "tofu init (providers)",
	"plan", "tofu plan",
	"apply", "tofu apply",
)

func (m *Manager) PlanPlatform(ctx context.Context, name string, sink Sink) (*tofu.PlanDiff, error) {
	r, err := m.platformRunner(ctx, name, sink)
	if err != nil {
		return nil, err
	}
	var diff *tofu.PlanDiff
	err = sink.Run("plan", func() error {
		sum, err := r.Plan(ctx)
		if err != nil {
			return err
		}
		if diff, err = r.ShowPlan(ctx, r.Warnings()); err != nil {
			return err
		}
		diff.SpecHash = r.specHash
		sink.Emit(Info, "plan", "", "%s", sum)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sink.Skip("apply")
	return diff, nil
}

func (m *Manager) ApplyPlatform(ctx context.Context, name string, sink Sink) error {
	r, err := m.platformRunner(ctx, name, sink)
	if err != nil {
		return err
	}
	var sum tofu.Summary
	err = sink.Run("plan", func() error {
		var err error
		sum, err = r.Plan(ctx)
		if err != nil {
			_ = m.Store.SetPlatformStatus(ctx, name, store.PlatformStatus{Error: err.Error()})
			return err
		}
		sink.Emit(Info, "plan", "", "%s", sum)
		return nil
	})
	if err != nil {
		return err
	}
	if sum.Empty() {
		if err := m.installSOPSKey(ctx, r.cluster, sink); err != nil {
			return err
		}
		sink.Emit(Info, "apply", "", "no changes")
		sink.Skip("apply")
		return m.recordPlatform(ctx, name, r.Runner, sum, sink)
	}
	return m.applyWith(ctx, r.cluster, r.Runner, sink)
}

func (m *Manager) applyWith(ctx context.Context, c *config.Cluster, r *tofu.Runner, sink Sink) error {
	name := c.Metadata.Name
	var sum tofu.Summary
	err := sink.Run("apply", func() error {
		if err := m.installSOPSKey(ctx, c, sink); err != nil {
			return err
		}
		var err error
		sum, err = r.Apply(ctx)
		if err != nil {
			_ = m.Store.SetPlatformStatus(ctx, name, store.PlatformStatus{Error: err.Error()})
		}
		return err
	})
	if err != nil {
		return err
	}
	return m.recordPlatform(ctx, name, r, sum, sink)
}

func (m *Manager) recordPlatform(ctx context.Context, name string, r *tofu.Runner, sum tofu.Summary, sink Sink) error {
	outputs, err := r.Outputs(ctx)
	if err != nil {
		return err
	}
	if err := m.Store.SetPlatformStatus(ctx, name, store.PlatformStatus{AppliedAt: time.Now().UTC().Format(time.RFC3339), Outputs: outputs}); err != nil {
		return err
	}
	if err := m.Store.SetClusterState(ctx, name, StateReady); err != nil {
		return err
	}
	_ = m.Store.Audit(ctx, name, "platform.apply", sum.String())
	for k, v := range outputs {
		if v != "" {
			sink.Emit(Info, "apply", "", "%s = %s", k, v)
		}
	}
	sink.Emit(Done, "apply", "", "platform converged: %s", sum)
	return nil
}

func tofuLogger(sink Sink) func(tofu.Line) {
	return func(l tofu.Line) {
		step := l.Phase
		switch {
		case l.Diagnostic != nil && l.Diagnostic.Severity == "error":
			sink.Emit(Error, step, "", "%s: %s", l.Diagnostic.Summary, l.Diagnostic.Detail)
		case l.Diagnostic != nil:
		case l.Type == "planned_change" && l.Change != nil:
			sink.Emit(Info, step, l.Change.Resource.Addr, "will %s", l.Change.Action)
		case l.Type == "apply_start" && l.Hook != nil:
			sink.Emit(Info, step, l.Hook.Resource.Addr, "%s...", l.Hook.Action)
		case l.Type == "apply_complete" && l.Hook != nil:
			sink.Emit(Info, step, l.Hook.Resource.Addr, "%s done in %ds", l.Hook.Action, l.Hook.ElapsedSeconds)
		case l.Type == "apply_errored" && l.Hook != nil:
			sink.Emit(Error, step, l.Hook.Resource.Addr, "%s failed", l.Hook.Action)
		}
	}
}

func (m *Manager) registryMirrorMissing(ctx context.Context, c *config.Cluster) string {
	want := []byte("http://" + net.JoinHostPort(c.RegistryIP(), fmt.Sprint(config.RegistryPort)))
	for _, n := range c.Spec.Nodes {
		if cfg, err := m.Store.GetNodeMachineConfig(ctx, n.IP); err != nil || !bytes.Contains(cfg, want) {
			return n.Hostname
		}
	}
	return ""
}
