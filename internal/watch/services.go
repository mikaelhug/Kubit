package watch

import (
	"fmt"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/store"
)

// Age gates keep rollouts, fresh creates and image pulls from alerting.
const (
	serviceAgeGate    = 5 * time.Minute
	crashloopWindow   = 10 * time.Minute
	crashloopRestarts = 3
)

// serviceResolves maps a recovery kind to the alert it clears; merged into resolves.
var serviceResolves = map[string]string{
	"workload.available": "workload.unavailable",
	"pod.recovered":      "pod.crashloop",
	"pvc.bound":          "pvc.pending",
	"service.endpoints":  "service.no-endpoints",
	"ingress.address":    "ingress.no-address",
	"lb.pool-free":       "lb.pool-exhausted",
	"flux.ready":         "flux.not-ready",
}

func init() {
	for k, v := range serviceResolves {
		resolves[k] = v
	}
}

// ServiceTracker holds the little state the workload rules need between collections:
// which objects currently have an open alert, how many consecutive collections a
// workload has been short, and recent restart counts per pod.
type ServiceTracker struct {
	open     map[string]string // object key → open alert kind
	bad      map[string]int    // object key → consecutive unhealthy collections
	good     map[string]int    // object key → consecutive healthy collections while an alert is open
	restarts map[string][]restartSample
	seeded   bool
}

// An alert opens after raiseAfter unhealthy collections and closes only after
// clearAfter healthy ones, so a pod restarting every minute is one open alert, not a
// warn/recovery pair per minute.
const (
	raiseAfter = 2
	clearAfter = 3
)

type restartSample struct {
	at    time.Time
	count int32
}

func NewServiceTracker() *ServiceTracker {
	return &ServiceTracker{open: map[string]string{}, bad: map[string]int{}, good: map[string]int{}, restarts: map[string][]restartSample{}}
}

// Seed marks alerts already open in the store so a daemon restart neither re-raises
// nor forgets them.
func (t *ServiceTracker) Seed(open []store.EventRow) {
	for _, e := range open {
		if _, ok := serviceAlertKinds[e.Kind]; ok {
			t.open[e.Node] = e.Kind
		}
	}
	t.seeded = true
}

var serviceAlertKinds = map[string]bool{"workload.unavailable": true, "pod.crashloop": true, "pvc.pending": true, "service.no-endpoints": true, "ingress.no-address": true, "lb.pool-exhausted": true, "flux.not-ready": true}

// Key is the object identifier stored in EventRow.Node: kind/namespace/name.
func Key(kind, ns, name string) string { return kind + "/" + ns + "/" + name }

// Derive applies the workload rules to one collection and returns the alerts to raise
// and the recoveries to record. ignore lists namespaces that never alert.
func (t *ServiceTracker) Derive(name string, cur *cluster.ServiceHealth, now time.Time, ignore []string) []store.EventRow {
	var out []store.EventRow
	skip := map[string]bool{}
	for _, ns := range ignore {
		skip[ns] = true
	}
	present := map[string]bool{}
	raise := func(key, kind, sev, msg string) {
		present[key] = true
		if t.open[key] == kind {
			return
		}
		t.open[key] = kind
		out = append(out, store.EventRow{Cluster: name, Node: key, Severity: sev, Kind: kind, Message: msg})
	}
	// ok marks an object healthy: it clears an open alert with the matching recovery.
	ok := func(key, recovery, msg string) {
		present[key] = true
		if kind, isOpen := t.open[key]; isOpen && serviceResolves[recovery] == kind {
			delete(t.open, key)
			out = append(out, store.EventRow{Cluster: name, Node: key, Severity: "info", Kind: recovery, Message: msg})
		}
	}
	// unhealthy counts a bad collection and raises once raiseAfter are consecutive.
	unhealthy := func(key, kind, sev, msg string) {
		delete(t.good, key)
		t.bad[key]++
		if t.bad[key] >= raiseAfter {
			raise(key, kind, sev, msg)
			return
		}
		present[key] = true
	}
	// healthy counts a good collection and resolves once clearAfter are consecutive.
	healthy := func(key, recovery, msg string) {
		delete(t.bad, key)
		present[key] = true
		if _, isOpen := t.open[key]; !isOpen {
			return
		}
		t.good[key]++
		if t.good[key] >= clearAfter {
			delete(t.good, key)
			ok(key, recovery, msg)
		}
	}
	gate := func(ageSec int64) bool { return time.Duration(ageSec)*time.Second >= serviceAgeGate }

	for _, w := range cur.Workloads {
		if skip[w.Namespace] || w.Kind == "Job" {
			continue
		}
		key := Key(w.Kind, w.Namespace, w.Name)
		obj := fmt.Sprintf("%s %s/%s", w.Kind, w.Namespace, w.Name)
		if w.Ready < w.Desired && w.Desired > 0 && gate(w.AgeSec) {
			unhealthy(key, "workload.unavailable", "warn", fmt.Sprintf("%s has %d/%d replicas ready", obj, w.Ready, w.Desired))
			continue
		}
		healthy(key, "workload.available", fmt.Sprintf("%s is available again (%d/%d)", obj, w.Ready, w.Desired))
	}

	for _, p := range cur.Pods {
		if skip[p.Namespace] {
			continue
		}
		key := Key("Pod", p.Namespace, p.Name)
		obj := fmt.Sprintf("pod %s/%s", p.Namespace, p.Name)
		hist := t.restarts[key]
		if len(hist) == 0 || hist[len(hist)-1].count != p.Restarts {
			// A first sighting with restarts counts as a restart "now": the pod must
			// then stay quiet for a full window before it is considered recovered.
			hist = append(hist, restartSample{at: now, count: p.Restarts})
		}
		cut := 0
		for cut < len(hist)-1 && now.Sub(hist[cut].at) > crashloopWindow {
			cut++
		}
		hist = hist[cut:]
		t.restarts[key] = hist
		burst := p.Restarts-hist[0].count >= crashloopRestarts
		quiet := p.Restarts == 0 || now.Sub(hist[len(hist)-1].at) >= crashloopWindow
		switch {
		case p.Phase == "CrashLoopBackOff" && gate(p.AgeSec):
			raise(key, "pod.crashloop", "warn", fmt.Sprintf("%s is in CrashLoopBackOff (%d restarts)%s", obj, p.Restarts, onNode(p.Node)))
		case burst && gate(p.AgeSec):
			raise(key, "pod.crashloop", "warn", fmt.Sprintf("%s restarted %d times in %s%s", obj, p.Restarts-hist[0].count, crashloopWindow, onNode(p.Node)))
		case (p.Phase == "Running" || p.Phase == "Succeeded") && quiet:
			// Between back-offs a crashing pod is briefly Running; only a full quiet
			// window counts as recovery.
			ok(key, "pod.recovered", fmt.Sprintf("%s is %s and has not restarted for %s", obj, p.Phase, crashloopWindow))
		default:
			present[key] = true
		}
	}

	for _, c := range cur.Claims {
		if skip[c.Namespace] {
			continue
		}
		key := Key("PersistentVolumeClaim", c.Namespace, c.Name)
		if c.Phase == "Pending" && gate(c.AgeSec) {
			raise(key, "pvc.pending", "warn", fmt.Sprintf("PVC %s/%s has been Pending for %s: no StorageClass or provisioner bound it", c.Namespace, c.Name, (time.Duration(c.AgeSec)*time.Second).Truncate(time.Minute)))
		} else if c.Phase == "Bound" {
			ok(key, "pvc.bound", fmt.Sprintf("PVC %s/%s is Bound", c.Namespace, c.Name))
		} else {
			present[key] = true
		}
	}

	for _, s := range cur.Services {
		if skip[s.Namespace] || !s.HasSelector || s.Type == "ExternalName" {
			continue
		}
		key := Key("Service", s.Namespace, s.Name)
		if s.Endpoints == 0 && gate(s.AgeSec) {
			unhealthy(key, "service.no-endpoints", "warn", fmt.Sprintf("Service %s/%s has no ready endpoints: its selector matches no running pod", s.Namespace, s.Name))
		} else if s.Endpoints > 0 {
			healthy(key, "service.endpoints", fmt.Sprintf("Service %s/%s has %d ready endpoint(s)", s.Namespace, s.Name, s.Endpoints))
		} else {
			present[key] = true
		}
	}

	if cur.MetalLB {
		for _, i := range cur.Ingresses {
			if skip[i.Namespace] {
				continue
			}
			key := Key("Ingress", i.Namespace, i.Name)
			if !i.HasAddress && gate(i.AgeSec) {
				unhealthy(key, "ingress.no-address", "warn", fmt.Sprintf("Ingress %s/%s has no address: no ingress controller claimed it", i.Namespace, i.Name))
			} else if i.HasAddress {
				healthy(key, "ingress.address", fmt.Sprintf("Ingress %s/%s has an address", i.Namespace, i.Name))
			} else {
				present[key] = true
			}
		}
	}

	for _, f := range cur.Flux {
		if skip[f.Namespace] || f.Suspended {
			continue
		}
		key := Key(f.Kind, f.Namespace, f.Name)
		obj := fmt.Sprintf("%s %s/%s", f.Kind, f.Namespace, f.Name)
		switch {
		case f.Ready == "False" && f.Reason == "DependencyNotReady":
			present[key] = true
		case f.Ready == "False":
			msg, _, _ := strings.Cut(f.Message, "\n")
			unhealthy(key, "flux.not-ready", "warn", fmt.Sprintf("%s is not ready: %s", obj, msg))
		case f.Ready == "True":
			healthy(key, "flux.ready", obj+" is ready again")
		default:
			present[key] = true
		}
	}

	if cur.Pool != nil && cur.Pool.Total > 0 {
		key := "MetalLB/pool/" + cur.Pool.Range
		if cur.Pool.Allocated >= cur.Pool.Total {
			raise(key, "lb.pool-exhausted", "critical", fmt.Sprintf("MetalLB pool %s is exhausted (%d/%d): new LoadBalancer services will stay Pending", cur.Pool.Range, cur.Pool.Allocated, cur.Pool.Total))
		} else {
			ok(key, "lb.pool-free", fmt.Sprintf("MetalLB pool %s has free addresses (%d/%d used)", cur.Pool.Range, cur.Pool.Allocated, cur.Pool.Total))
		}
	}

	// Objects that vanished take their alert with them.
	for key, kind := range t.open {
		if present[key] {
			continue
		}
		delete(t.open, key)
		delete(t.bad, key)
		delete(t.good, key)
		delete(t.restarts, key)
		if rec := recoveryFor(kind); rec != "" {
			out = append(out, store.EventRow{Cluster: name, Node: key, Severity: "info", Kind: rec, Message: key + " was deleted"})
		}
	}
	for key := range t.restarts {
		if !present[key] {
			delete(t.restarts, key)
		}
	}
	return out
}

func recoveryFor(alert string) string {
	for rec, a := range serviceResolves {
		if a == alert {
			return rec
		}
	}
	return ""
}

func onNode(n string) string {
	if n == "" {
		return ""
	}
	return " on " + n
}
