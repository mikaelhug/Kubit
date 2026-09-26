package cluster

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/tofu"
)

// ClusterDir is where a cluster's on-disk artefacts live: kubeconfig, talosconfig,
// infra/platform (executed), infra/talos (export only).
func (m *Manager) ClusterDir(name string) string {
	return filepath.Join(m.Home, "clusters", name)
}

// writeCredentials materialises kubeconfig and talosconfig for tools that read files
// (OpenTofu providers, kubectl, talosctl).
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

func (m *Manager) platformRunner(ctx context.Context, name string, sink Sink) (*tofu.Runner, error) {
	sink.plan(platformSteps...)
	sink.begin("render")
	c, row, err := m.LoadCluster(ctx, name)
	if err != nil {
		return nil, err
	}
	if row.State != StateBootstrapped && row.State != StateReady {
		return nil, fmt.Errorf("cluster %s is %s; the platform needs a bootstrapped cluster", name, row.State)
	}
	if c.Spec.Platform.Longhorn.Enabled {
		if id, pools, err := m.desiredSchematics(ctx, c); err != nil {
			sink.emit(Warn, "render", "", "could not check the node image for Longhorn's extensions: %v", err)
		} else if imageOutdated(c, id, pools) {
			return nil, fmt.Errorf("Longhorn needs the Talos extensions %s on every node first: upgrade Talos (Lifecycle), then plan again", strings.Join(config.LonghornExtensions, " and "))
		}
	}
	kubeconfig, err := m.writeCredentials(ctx, name)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(m.ClusterDir(name), "infra", "platform")
	if err := tofu.Render(dir, c, kubeconfig); err != nil {
		return nil, err
	}
	bin, err := tofu.Binary(ctx, filepath.Join(m.Home, "bin"))
	if err != nil {
		return nil, err
	}
	sink.emit(Info, "render", "", "rendered %s (tofu %s)", dir, bin)
	if c.Spec.Platform.Flux.Enabled {
		k, err := m.SOPSKey(ctx, name)
		if err != nil {
			return nil, err
		}
		sink.emit(Info, "render", "", "SOPS key %s goes to %s/%s on apply", k.Recipient, tofu.SOPSNamespace, tofu.SOPSSecret)
	}
	sink.end("render")
	r := &tofu.Runner{Bin: bin, Dir: dir, Log: tofuLogger(sink)}
	if err := sink.run("init", func() error { return r.Init(ctx) }); err != nil {
		return nil, err
	}
	return r, nil
}

var platformSteps = Steps(
	"render", "Render infra/platform from cluster.yaml",
	"init", "tofu init (providers)",
	"plan", "tofu plan",
	"apply", "tofu apply",
)

// PlanPlatform renders, initialises and plans; the reviewable diff is returned and
// plan.tfplan stays in the module directory for ApplyPlan.
func (m *Manager) PlanPlatform(ctx context.Context, name string, sink Sink) (*tofu.PlanDiff, error) {
	r, err := m.platformRunner(ctx, name, sink)
	if err != nil {
		return nil, err
	}
	var diff *tofu.PlanDiff
	err = sink.run("plan", func() error {
		sum, err := r.Plan(ctx)
		if err != nil {
			return err
		}
		if diff, err = r.ShowPlan(ctx, r.Warnings()); err != nil {
			return err
		}
		sink.emit(Info, "plan", "", "%s", sum)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sink.skip("apply")
	return diff, nil
}

// ErrStalePlan is returned when cluster.yaml changed after the plan was made.
var ErrStalePlan = fmt.Errorf("plan is stale: cluster.yaml changed since it was made; plan again")

// ApplyPlan executes a previously reviewed plan.tfplan. planTime is the plan's tofu
// timestamp; the cluster row's updated_at must not be newer.
func (m *Manager) ApplyPlan(ctx context.Context, name string, planTime string, sink Sink) error {
	sink.plan(platformSteps...)
	sink.skip("render")
	sink.skip("plan")
	row, err := m.Store.GetCluster(ctx, name)
	if err != nil {
		return err
	}
	if planTime != "" && row.UpdatedAt > planTime {
		return ErrStalePlan
	}
	dir := filepath.Join(m.ClusterDir(name), "infra", "platform")
	if _, err := os.Stat(filepath.Join(dir, "plan.tfplan")); err != nil {
		return fmt.Errorf("no saved plan for %s; plan first", name)
	}
	bin, err := tofu.Binary(ctx, filepath.Join(m.Home, "bin"))
	if err != nil {
		return err
	}
	r := &tofu.Runner{Bin: bin, Dir: dir, Log: tofuLogger(sink)}
	if err := sink.run("init", func() error { return r.Init(ctx) }); err != nil {
		return err
	}
	return m.applyWith(ctx, name, r, sink)
}

// ApplyPlatform converges the in-cluster layer on cluster.yaml's platform section in
// one go (plan and apply without a review), used by the CLI and by cluster creation.
func (m *Manager) ApplyPlatform(ctx context.Context, name string, sink Sink) error {
	r, err := m.platformRunner(ctx, name, sink)
	if err != nil {
		return err
	}
	var sum tofu.Summary
	err = sink.run("plan", func() error {
		var err error
		sum, err = r.Plan(ctx)
		if err != nil {
			_ = m.Store.SetPlatformStatus(ctx, name, store.PlatformStatus{Error: err.Error()})
			return err
		}
		sink.emit(Info, "plan", "", "%s", sum)
		return nil
	})
	if err != nil {
		return err
	}
	if sum.Empty() {
		if err := m.installSOPSKey(ctx, name, sink); err != nil {
			return err
		}
		sink.emit(Info, "apply", "", "no changes")
		sink.skip("apply")
		return m.recordPlatform(ctx, name, r, sum, sink)
	}
	return m.applyWith(ctx, name, r, sink)
}

func (m *Manager) applyWith(ctx context.Context, name string, r *tofu.Runner, sink Sink) error {
	var sum tofu.Summary
	err := sink.run("apply", func() error {
		if err := m.installSOPSKey(ctx, name, sink); err != nil {
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
			sink.emit(Info, "apply", "", "%s = %s", k, v)
		}
	}
	sink.emit(Done, "apply", "", "platform converged: %s", sum)
	return nil
}

// tofuLogger turns tofu's machine-readable lines into operation events.
func tofuLogger(sink Sink) func(tofu.Line) {
	return func(l tofu.Line) {
		step := l.Phase
		switch {
		case l.Diagnostic != nil && l.Diagnostic.Severity == "error":
			sink.emit(Error, step, "", "%s: %s", l.Diagnostic.Summary, l.Diagnostic.Detail)
		case l.Diagnostic != nil:
			// Warnings (deprecations) are collected into the plan review instead.
		case l.Type == "planned_change" && l.Change != nil:
			sink.emit(Info, step, l.Change.Resource.Addr, "will %s", l.Change.Action)
		case l.Type == "apply_start" && l.Hook != nil:
			sink.emit(Info, step, l.Hook.Resource.Addr, "%s...", l.Hook.Action)
		case l.Type == "apply_complete" && l.Hook != nil:
			sink.emit(Info, step, l.Hook.Resource.Addr, "%s done in %ds", l.Hook.Action, l.Hook.ElapsedSeconds)
		case l.Type == "apply_errored" && l.Hook != nil:
			sink.emit(Error, step, l.Hook.Resource.Addr, "%s failed", l.Hook.Action)
		}
	}
}
