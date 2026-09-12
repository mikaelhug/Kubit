package cluster

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

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
	c, row, err := m.LoadCluster(ctx, name)
	if err != nil {
		return nil, err
	}
	if row.State != StateBootstrapped && row.State != StateReady {
		return nil, fmt.Errorf("cluster %s is %s; the platform needs a bootstrapped cluster", name, row.State)
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
	sink.emit(Info, "platform", "", "rendered %s (tofu %s)", dir, bin)
	r := &tofu.Runner{Bin: bin, Dir: dir, Log: func(l tofu.Line) {
		switch {
		case l.Diagnostic != nil && l.Diagnostic.Severity == "error":
			sink.emit(Error, "tofu", "", "%s: %s", l.Diagnostic.Summary, l.Diagnostic.Detail)
		case l.Diagnostic != nil:
			sink.emit(Warn, "tofu", "", "%s", l.Diagnostic.Summary)
		case l.Type == "planned_change" && l.Change != nil:
			sink.emit(Info, "tofu", l.Change.Resource.Addr, "will %s", l.Change.Action)
		case l.Type == "apply_start" && l.Hook != nil:
			sink.emit(Info, "tofu", l.Hook.Resource.Addr, "%s...", l.Hook.Action)
		case l.Type == "apply_complete" && l.Hook != nil:
			sink.emit(Info, "tofu", l.Hook.Resource.Addr, "%s done in %ds", l.Hook.Action, l.Hook.ElapsedSeconds)
		case l.Type == "apply_errored" && l.Hook != nil:
			sink.emit(Error, "tofu", l.Hook.Resource.Addr, "%s failed", l.Hook.Action)
		case l.Type == "change_summary":
			sink.emit(Info, "tofu", "", "%s", l.Message)
		}
	}}
	if err := r.Init(ctx); err != nil {
		return nil, err
	}
	return r, nil
}

// PlanPlatform renders and plans without applying; an empty summary means no drift.
func (m *Manager) PlanPlatform(ctx context.Context, name string, sink Sink) (tofu.Summary, error) {
	r, err := m.platformRunner(ctx, name, sink)
	if err != nil {
		return tofu.Summary{}, err
	}
	return r.Plan(ctx)
}

// ApplyPlatform converges the in-cluster layer on cluster.yaml's platform section.
func (m *Manager) ApplyPlatform(ctx context.Context, name string, sink Sink) error {
	r, err := m.platformRunner(ctx, name, sink)
	if err != nil {
		return err
	}
	sum, err := r.Plan(ctx)
	if err != nil {
		_ = m.Store.SetPlatformStatus(ctx, name, store.PlatformStatus{Error: err.Error()})
		return err
	}
	if sum.Empty() {
		sink.emit(Info, "platform", "", "no changes")
	} else {
		sink.emit(Info, "platform", "", "applying: %s", sum)
		if _, err := r.Apply(ctx); err != nil {
			_ = m.Store.SetPlatformStatus(ctx, name, store.PlatformStatus{Error: err.Error()})
			return err
		}
	}
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
		if v != "" && k != "argocd_admin_password" {
			sink.emit(Info, "platform", "", "%s = %s", k, v)
		}
	}
	sink.emit(Done, "platform", "", "platform applied")
	return nil
}
