package cluster

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/k8s"
	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/compatibility"
	"github.com/siderolabs/talos/pkg/machinery/gendata"
	utilversion "k8s.io/apimachinery/pkg/util/version"
)

const minVarFree = 1 << 30

var upgradePrechecks = Steps("precheck", "Pre-flight: etcd, node health, disk headroom", "snapshot", "Take a pre-upgrade etcd snapshot")

func (m *Manager) precheckUpgrade(ctx context.Context, c *config.Cluster, kc *k8s.Client, talosconfig []byte, kind, kubeFrom, target string, sink Sink) error {
	if err := checkUpgradeVersions(c, kind, kubeFrom, target, sink); err != nil {
		return err
	}
	name := c.Metadata.Name
	var problems []string
	st, err := m.Status(ctx, name)
	if err != nil {
		return err
	}
	if !st.APIReachable {
		problems = append(problems, "Kubernetes API unreachable: "+st.APIError)
	}
	if !st.Etcd.Healthy {
		problems = append(problems, fmt.Sprintf("etcd unhealthy (%d/%d members)", st.Etcd.Members, st.Etcd.Expected))
	}
	for _, n := range st.Nodes {
		switch {
		case !n.TalosReachable:
			problems = append(problems, fmt.Sprintf("%s: Talos API unreachable (%s)", n.Hostname, n.TalosError))
		case st.APIReachable && !n.Ready:
			problems = append(problems, fmt.Sprintf("%s: not Ready", n.Hostname))
		case n.Unschedulable:
			problems = append(problems, fmt.Sprintf("%s: cordoned — uncordon before upgrading", n.Hostname))
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	sink.Emit(Info, "precheck", "", "etcd healthy, %d/%d nodes Ready and reachable", st.Totals.NodesReady, st.Totals.Nodes)

	if problems := m.checkVarHeadroom(ctx, c, talosconfig, sink); len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}

	if kind == "talos" {
		versions, err := m.Factory.Versions(ctx)
		if err != nil {
			sink.Emit(Warn, "precheck", "", "could not list Image Factory versions: %v", err)
		} else if !slices.Contains(versions, target) {
			return fmt.Errorf("Talos %s is not published by the Image Factory (%s); latest: %s", target, m.Factory.BaseURL(), latestOf(versions))
		} else {
			sink.Emit(Info, "precheck", "", "Talos %s is available from the Image Factory", target)
		}
	}

	if kind == "kubernetes" {
		used, err := kc.DeprecatedAPIs(ctx)
		if err != nil {
			sink.Emit(Warn, "precheck", "", "deprecated-API scan skipped: %v", err)
		} else {
			var removed, deprecated []string
			for _, d := range used {
				if d.RemovedBy(target) {
					removed = append(removed, d.String())
				} else {
					deprecated = append(deprecated, d.String())
				}
			}
			if len(deprecated) > 0 {
				sink.Emit(Warn, "precheck", "", "deprecated APIs in use (still served by %s): %s", target, strings.Join(deprecated, ", "))
			}
			if len(removed) > 0 {
				return fmt.Errorf("APIs still in use are removed in %s: %s — migrate the clients first (kubectl get --raw /metrics | grep requested_deprecated_apis)", target, strings.Join(removed, ", "))
			}
			sink.Emit(Info, "precheck", "", "no API in use is removed by %s (%d deprecated group/versions seen since the API server started)", target, len(used))
		}
	}
	return nil
}

func (m *Manager) checkVarHeadroom(ctx context.Context, c *config.Cluster, talosconfig []byte, sink Sink) []string {
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		problems []string
	)
	for _, n := range c.Spec.Nodes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			problem := m.varHeadroom(ctx, c.Metadata.Name, n, talosconfig, sink)
			if problem != "" {
				mu.Lock()
				problems = append(problems, problem)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	sort.Strings(problems)
	return problems
}

func (m *Manager) varHeadroom(ctx context.Context, name string, n config.Node, talosconfig []byte, sink Sink) string {
	tc, err := m.talosClientFor(name, n.IP, talosconfig)
	if err != nil {
		return fmt.Sprintf("%s: %v", n.Hostname, err)
	}
	call, cancel := context.WithTimeout(ctx, 15*time.Second)
	avail, size, err := tc.VarAvailable(call)
	cancel()
	if err != nil {
		m.noteTalosErr(name, n.IP, tc, err)
		sink.Emit(Warn, "precheck", n.Hostname, "could not read /var usage: %v", err)
		return ""
	}
	if avail < minVarFree {
		return fmt.Sprintf("%s: only %s free of %s on /var (need %s)", n.Hostname, HumanBytes(avail), HumanBytes(size), HumanBytes(minVarFree))
	}
	sink.Emit(Info, "precheck", n.Hostname, "/var: %s free of %s", HumanBytes(avail), HumanBytes(size))
	return ""
}

func checkUpgradeVersions(c *config.Cluster, kind, kubeFrom, target string, sink Sink) error {
	talosVersion, kubeVersion := c.Spec.TalosVersion, target
	if kind == "talos" {
		talosVersion, kubeVersion = target, kubeFrom
	} else if err := checkKubernetesStep(kubeFrom, target); err != nil {
		return err
	}
	known, err := kubernetesSupported(talosVersion, kubeVersion)
	switch {
	case err != nil:
		return err
	case !known:
		sink.Emit(Warn, "precheck", "", "Talos %s is newer than Kubit knows; Kubernetes %s compatibility unchecked", talosVersion, kubeVersion)
	default:
		sink.Emit(Info, "precheck", "", "Talos %s supports Kubernetes %s", talosVersion, kubeVersion)
	}
	return nil
}

func checkKubernetesStep(from, to string) error {
	target, err := utilversion.ParseSemantic(to)
	if err != nil {
		return fmt.Errorf("%s is not a Kubernetes version (want vMAJOR.MINOR.PATCH)", to)
	}
	current, err := utilversion.ParseGeneric(from)
	if err != nil {
		return fmt.Errorf("current Kubernetes version %q unreadable: %w", from, err)
	}
	switch {
	case target.LessThan(current):
		return fmt.Errorf("Kubernetes %s is older than the running %s; downgrades are not supported", to, from)
	case target.Major() != current.Major() || target.Minor() > current.Minor()+1:
		return fmt.Errorf("Kubernetes %s skips a minor version from %s; upgrade one minor at a time (v%d.%d first)", to, from, current.Major(), current.Minor()+1)
	}
	return nil
}

func kubernetesSupported(talosVersion, kubeVersion string) (known bool, err error) {
	tv, err := utilversion.ParseGeneric(talosVersion)
	if err != nil {
		return false, fmt.Errorf("Talos version %q unreadable: %w", talosVersion, err)
	}
	if tv.GreaterThan(utilversion.MustParseGeneric(gendata.VersionTag)) {
		return false, nil
	}
	t, err := compatibility.ParseTalosVersion(&machineapi.VersionInfo{Tag: talosVersion})
	if err != nil {
		return false, fmt.Errorf("Talos version %q unreadable: %w", talosVersion, err)
	}
	k, err := compatibility.ParseKubernetesVersion(kubeVersion)
	if err != nil {
		return false, fmt.Errorf("Kubernetes version %q unreadable: %w", kubeVersion, err)
	}
	return true, k.SupportedWith(t)
}

func (m *Manager) preUpgradeSnapshot(ctx context.Context, name string, sink Sink) error {
	sn, err := m.SnapshotEtcd(ctx, name, "pre-upgrade", subSink(sink, "snapshot"))
	if err != nil {
		return fmt.Errorf("pre-upgrade snapshot: %w", err)
	}
	sink.Emit(Info, "snapshot", "", "etcd snapshot #%d stored (%d keys); restore from Backups if the upgrade goes wrong", sn.ID, sn.Keys)
	return nil
}

func latestOf(versions []string) string {
	for i := len(versions) - 1; i >= 0; i-- {
		if !strings.Contains(versions[i], "-") {
			return versions[i]
		}
	}
	if len(versions) > 0 {
		return versions[len(versions)-1]
	}
	return "?"
}
