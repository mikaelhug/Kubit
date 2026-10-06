package cluster

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/tofu"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (m *Manager) tofuBin(ctx context.Context) (string, error) {
	return tofu.Binary(ctx, filepath.Join(m.Home, "bin"))
}

type stateMove int

const (
	stateInCluster stateMove = iota
	stateFromRepo
	stateRepoStale
)

func repoStateName(d *Desired) string {
	if rel, err := filepath.Rel(d.Dir, d.RepoState); err == nil {
		return rel
	}
	return d.RepoState
}

func (s stateMove) done(d *Desired) string {
	switch s {
	case stateFromRepo:
		return "moved " + repoStateName(d) + " into the cluster (" + tofu.StateNamespace + "/" + tofu.StateSecret + "); commit its removal"
	case stateRepoStale:
		return "removed " + repoStateName(d) + "; the cluster holds the platform state; commit its removal"
	}
	return ""
}

func (s stateMove) change(d *Desired) (tofu.Group, bool) {
	rel := repoStateName(d)
	switch s {
	case stateFromRepo:
		return tofu.Group{Addon: "state", Actions: "Move the add-on state from " + rel + " into the cluster (" + tofu.StateNamespace + "/" + tofu.StateSecret + "). Nothing in the cluster changes."}, true
	case stateRepoStale:
		return tofu.Group{Addon: "state", Actions: "delete " + rel + "; the cluster already holds the add-on state"}, true
	}
	return tofu.Group{}, false
}

func (m *Manager) stateMoveFor(ctx context.Context, d *Desired) (stateMove, error) {
	if _, err := os.Stat(d.RepoState); err != nil {
		return stateInCluster, nil
	}
	kc, err := m.KubeClientFor(d.Cluster.Metadata.Name, m.liveKubeconfig(d))
	if err != nil {
		return 0, err
	}
	call, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, err = kc.CoreV1().Secrets(tofu.StateNamespace).Get(call, tofu.StateSecret, metav1.GetOptions{})
	switch {
	case err == nil:
		return stateRepoStale, nil
	case apierrors.IsNotFound(err):
		return stateFromRepo, nil
	}
	return 0, fmt.Errorf("platform state: %w", err)
}

func (m *Manager) openPlatform(ctx context.Context, d *Desired, c *config.Cluster, apply bool, log func(tofu.Line)) (*tofu.Runner, stateMove, error) {
	move, err := m.stateMoveFor(ctx, d)
	if err != nil {
		return nil, 0, err
	}
	bin, err := m.tofuBin(ctx)
	if err != nil {
		return nil, 0, err
	}
	r, err := tofu.New(bin, filepath.Join(m.Home, "plugins"), log)
	if err != nil {
		return nil, 0, err
	}
	r.Passphrase = d.Passphrase
	if d.BackupKey != "" || d.BackupSec != "" {
		r.Env = []string{"TF_VAR_backup_access_key_id=" + d.BackupKey, "TF_VAR_backup_secret_access_key=" + d.BackupSec}
	}
	if err := m.preparePlatform(ctx, r, d, c, move, apply); err != nil {
		r.Close()
		return nil, 0, err
	}
	return r, move, nil
}

func (m *Manager) preparePlatform(ctx context.Context, r *tofu.Runner, d *Desired, c *config.Cluster, move stateMove, apply bool) error {
	kubeconfig := m.liveKubeconfig(d)
	if err := r.WriteKubeconfig(kubeconfig); err != nil {
		return err
	}
	if err := tofu.Render(r.Dir, c, r.Kubeconfig, m.ingressPin(ctx, c.Metadata.Name, kubeconfig)); err != nil {
		return err
	}
	switch {
	case move == stateFromRepo && apply:
		if err := r.MigrateFrom(ctx, d.RepoState); err != nil {
			return err
		}
		return removeRepoState(d.RepoState)
	case move == stateFromRepo:
		return r.InitFrom(ctx, d.RepoState)
	case move == stateRepoStale && apply:
		if err := r.Init(ctx); err != nil {
			return err
		}
		return removeRepoState(d.RepoState)
	}
	return r.Init(ctx)
}

func removeRepoState(path string) error {
	if err := os.Remove(path); err != nil {
		return err
	}
	_ = os.Remove(path + ".backup")
	_ = os.Remove(filepath.Dir(path))
	return nil
}

const metallbPin = "metallb.io/loadBalancerIPs"

func (m *Manager) ingressPin(ctx context.Context, name string, kubeconfig []byte) string {
	kc, err := m.KubeClientFor(name, kubeconfig)
	if err != nil {
		return ""
	}
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	svc, err := kc.CoreV1().Services("traefik").Get(call, "traefik", metav1.GetOptions{})
	if err != nil {
		return ""
	}
	if pin := svc.Annotations[metallbPin]; pin != "" {
		return pin
	}
	for _, in := range svc.Status.LoadBalancer.Ingress {
		if in.IP != "" {
			return in.IP
		}
	}
	return ""
}

func (m *Manager) planPlatformFor(ctx context.Context, d *Desired, applied *config.Cluster, readOnly bool) ([]tofu.Group, error) {
	c := d.Cluster.Clone()
	c.Spec.Nodes = applied.Spec.Nodes
	r, move, err := m.openPlatform(ctx, d, c, false, nil)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	r.ReadOnly = readOnly
	if _, err := r.Plan(ctx); err != nil {
		return nil, err
	}
	groups, err := r.ShowPlan(ctx)
	if err != nil {
		return nil, err
	}
	if g, ok := move.change(d); ok {
		groups = append([]tofu.Group{g}, groups...)
	}
	return groups, nil
}

func (m *Manager) ApplyPlatform(ctx context.Context, name string, sink Sink) error {
	c, _, err := m.LoadCluster(name)
	if err != nil {
		return err
	}
	d, err := m.Desired(name)
	if err != nil {
		return err
	}
	if c.Spec.Platform.Longhorn.Enabled {
		if id, err := m.Factory.CreateSchematic(ctx, c.Spec.Extensions); err != nil {
			sink.Emit(Warn, "init", "", "could not check the node image for Longhorn's extensions: %v", err)
		} else if id != c.Spec.SchematicID {
			return fmt.Errorf("Longhorn needs the Talos extensions %s on every node; kubit apply re-images the nodes first", strings.Join(config.LonghornExtensions, " and "))
		}
	}
	var (
		r    *tofu.Runner
		move stateMove
	)
	if err := sink.Run("init", func() error {
		r, move, err = m.openPlatform(ctx, d, c, true, tofuLogger(sink))
		if msg := move.done(d); err == nil && msg != "" {
			sink.Emit(Warn, "init", "", "%s", msg)
		}
		return err
	}); err != nil {
		return err
	}
	defer r.Close()
	var sum tofu.Summary
	if err := sink.Run("plan", func() error {
		sum, err = r.Plan(ctx)
		if err == nil {
			sink.Emit(Info, "plan", "", "%s", sum)
		}
		return err
	}); err != nil {
		return err
	}
	if err := sink.Run("apply", func() error {
		if err := m.installSOPSKey(ctx, c, sink); err != nil {
			return err
		}
		if sum.Empty() {
			sink.Emit(Info, "apply", "", "no changes")
			return nil
		}
		sum, err = r.Apply(ctx)
		return err
	}); err != nil {
		return err
	}
	outputs, err := r.Outputs(ctx)
	if err != nil {
		return err
	}
	for k, v := range outputs {
		if v != "" {
			sink.Emit(Info, "apply", "", "%s = %s", k, v)
		}
	}
	sink.Emit(Done, "apply", "", "platform converged: %s", sum)
	m.Store.SetClusterState(name, StateReady)
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
		case l.Level == "warn":
			sink.Emit(Warn, step, "", "%s", l.Message)
		}
	}
}
