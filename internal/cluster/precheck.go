package cluster

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/k8s"
	"github.com/mikael/kubit/internal/talos"
)

// minVarFree is the /var headroom an upgrade needs: the new Talos image or the new
// Kubernetes component images are pulled there before anything is swapped.
const minVarFree = 1 << 30

// upgradePrechecks are the steps every rolling upgrade starts with.
var upgradePrechecks = Steps("precheck", "Pre-flight: etcd, node health, disk headroom", "snapshot", "Take a pre-upgrade etcd snapshot")

// precheckUpgrade refuses to start an upgrade on a cluster that is not fully healthy.
// kind is "talos" or "kubernetes"; for Kubernetes upgrades the API server's record of
// deprecated API usage is checked against the target release.
func (m *Manager) precheckUpgrade(ctx context.Context, c *config.Cluster, kc *k8s.Client, talosconfig []byte, kind, target string, sink Sink) error {
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
	sink.emit(Info, "precheck", "", "etcd healthy, %d/%d nodes Ready and reachable", st.Totals.NodesReady, st.Totals.Nodes)

	for _, n := range c.Spec.Nodes {
		dial, cancel := context.WithTimeout(ctx, 15*time.Second)
		tc, err := talos.Dial(dial, n.IP, talosconfig)
		cancel()
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", n.Hostname, err))
			continue
		}
		call, cancel := context.WithTimeout(ctx, 15*time.Second)
		avail, size, err := tc.VarAvailable(call)
		cancel()
		tc.Close()
		if err != nil {
			sink.emit(Warn, "precheck", n.Hostname, "could not read /var usage: %v", err)
			continue
		}
		if avail < minVarFree {
			problems = append(problems, fmt.Sprintf("%s: only %s free of %s on /var (need %s)", n.Hostname, humanBytes(avail), humanBytes(size), humanBytes(minVarFree)))
		} else {
			sink.emit(Info, "precheck", n.Hostname, "/var: %s free of %s", humanBytes(avail), humanBytes(size))
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}

	if kind == "talos" {
		versions, err := m.Factory.Versions(ctx)
		if err != nil {
			sink.emit(Warn, "precheck", "", "could not list Image Factory versions: %v", err)
		} else if !slices.Contains(versions, target) {
			return fmt.Errorf("Talos %s is not published by the Image Factory (%s); latest: %s", target, m.Factory.BaseURL, latestOf(versions))
		} else {
			sink.emit(Info, "precheck", "", "Talos %s is available from the Image Factory", target)
		}
	}

	if kind == "kubernetes" {
		used, err := kc.DeprecatedAPIs(ctx)
		if err != nil {
			sink.emit(Warn, "precheck", "", "deprecated-API scan skipped: %v", err)
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
				sink.emit(Warn, "precheck", "", "deprecated APIs in use (still served by %s): %s", target, strings.Join(deprecated, ", "))
			}
			if len(removed) > 0 {
				return fmt.Errorf("APIs still in use are removed in %s: %s — migrate the clients first (kubectl get --raw /metrics | grep requested_deprecated_apis)", target, strings.Join(removed, ", "))
			}
			sink.emit(Info, "precheck", "", "no API in use is removed by %s (%d deprecated group/versions seen since the API server started)", target, len(used))
		}
	}
	return nil
}

// preUpgradeSnapshot takes the safety snapshot; failure to snapshot stops the upgrade.
func (m *Manager) preUpgradeSnapshot(ctx context.Context, name string, sink Sink) error {
	sn, err := m.SnapshotEtcd(ctx, name, "pre-upgrade", subSink(sink, "snapshot"))
	if err != nil {
		return fmt.Errorf("pre-upgrade snapshot: %w", err)
	}
	sink.emit(Info, "snapshot", "", "etcd snapshot #%d stored (%d keys); restore from Backups if the upgrade goes wrong", sn.ID, sn.Keys)
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
