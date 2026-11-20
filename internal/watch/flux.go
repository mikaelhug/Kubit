package watch

import (
	"context"
	"slices"
	"strings"

	"github.com/mikaelhug/kubit/internal/cluster"
	"github.com/mikaelhug/kubit/internal/k8s"
)

const fluxFailing = "flux.failing"

func (w *Watcher) fluxFailing(ctx context.Context, name string, prev, st *cluster.Status) []k8s.FluxObject {
	var before []k8s.FluxObject
	if prev != nil {
		before = prev.Flux
	}
	if !st.APIReachable {
		return before
	}
	kc, _, err := w.kubeClient(ctx, name)
	if err != nil {
		return before
	}
	objs, err := kc.FluxObjects(ctx)
	if err != nil {
		return before
	}
	return failingFlux(objs, before)
}

func failingFlux(objs, before []k8s.FluxObject) []k8s.FluxObject {
	var out []k8s.FluxObject
	for _, o := range objs {
		if o.Suspended {
			continue
		}
		// Flux marks dependents DependencyNotReady while the object they wait on reconciles or fails.
		if o.Ready == "False" && o.Reason != "DependencyNotReady" {
			out = append(out, o)
		} else if o.Ready != "True" {
			if i := slices.IndexFunc(before, func(b k8s.FluxObject) bool { return fluxRef(b) == fluxRef(o) }); i >= 0 {
				out = append(out, before[i])
			}
		}
	}
	return out
}

func fluxFacts(name string, cur *cluster.Status) map[string]Event {
	out := map[string]Event{}
	if cur.Off {
		return out
	}
	for _, o := range cur.Flux {
		ref := fluxRef(o)
		out[factKey(fluxFailing, ref)] = Event{Cluster: name, Node: ref, Severity: "warn", Kind: fluxFailing, Message: fluxName(ref) + ": " + fluxSummary(o), Detail: strings.TrimSpace(o.Message)}
	}
	return out
}

func fluxRef(o k8s.FluxObject) string { return o.Kind + "/" + o.Namespace + "/" + o.Name }

func fluxName(ref string) string {
	kind, rest, _ := strings.Cut(ref, "/")
	if ns, name, ok := strings.Cut(rest, "/"); ok && ns == "flux-system" {
		rest = name
	}
	return kind + " " + rest
}

const summaryLimit = 180

func fluxSummary(o k8s.FluxObject) string {
	line, _, _ := strings.Cut(strings.TrimSpace(o.Message), "\n")
	if line == "" {
		line = o.Reason
	}
	if subject, rest, ok := strings.Cut(line, " dry-run failed"); ok {
		if _, why, ok := strings.Cut(rest, " is invalid: "); ok {
			rest = why
		} else if _, why, ok := strings.Cut(rest, "): "); ok {
			rest = why
		}
		line = subject + " rejected: " + rest
	}
	if r := []rune(line); len(r) > summaryLimit {
		line = strings.TrimRight(string(r[:summaryLimit]), " ,:;") + "…"
	}
	return line
}
