package watch

import (
	"fmt"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/store"
)

const (
	serviceAgeGate    = 5 * time.Minute
	crashloopWindow   = 10 * time.Minute
	crashloopRestarts = 3
)

var serviceResolves = map[string]string{
	"workload.available": "workload.unavailable",
	"pod.recovered":      "pod.crashloop",
	"pvc.bound":          "pvc.pending",
	"service.endpoints":  "service.no-endpoints",
	"ingress.address":    "ingress.no-address",
	"lb.pool-free":       "lb.pool-exhausted",
	"flux.ready":         "flux.not-ready",
}

type ServiceTracker struct {
	open     map[string]string
	bad      map[string]int
	good     map[string]int
	restarts map[string][]restartSample
	seeded   bool
}

const (
	raiseAfter = 2
	clearAfter = 3
)

type restartSample struct {
	at    time.Time
	count int32
}

func newServiceTracker() *ServiceTracker {
	return &ServiceTracker{open: map[string]string{}, bad: map[string]int{}, good: map[string]int{}, restarts: map[string][]restartSample{}}
}

func (t *ServiceTracker) Seed(open []store.EventRow) {
	for _, e := range open {
		if serviceAlertKinds[e.Kind] {
			t.open[e.Node] = e.Kind
		}
	}
	t.seeded = true
}

var serviceAlertKinds = func() map[string]bool {
	m := map[string]bool{}
	for _, alert := range serviceResolves {
		m[alert] = true
	}
	return m
}()

func serviceKey(kind, ns, name string) string { return kind + "/" + ns + "/" + name }

type derivation struct {
	t       *ServiceTracker
	name    string
	now     time.Time
	skip    map[string]bool
	present map[string]bool
	out     []store.EventRow
}

func (t *ServiceTracker) Derive(name string, cur *cluster.ServiceHealth, now time.Time, ignore []string) []store.EventRow {
	d := &derivation{t: t, name: name, now: now, skip: map[string]bool{}, present: map[string]bool{}}
	for _, ns := range ignore {
		d.skip[ns] = true
	}
	d.workloads(cur.Workloads)
	d.pods(cur.Pods)
	d.claims(cur.Claims)
	d.services(cur.Services)
	if cur.MetalLB {
		d.ingresses(cur.Ingresses)
	}
	d.flux(cur.Flux)
	if cur.Pool != nil && cur.Pool.Total > 0 {
		d.pool(cur.Pool)
	}
	d.sweep()
	return d.out
}

func (d *derivation) raise(key, kind, sev, msg string) {
	d.present[key] = true
	if d.t.open[key] == kind {
		return
	}
	d.t.open[key] = kind
	d.out = append(d.out, store.EventRow{Cluster: d.name, Node: key, Severity: sev, Kind: kind, Message: msg})
}

func (d *derivation) ok(key, recovery, msg string) {
	d.present[key] = true
	if kind, isOpen := d.t.open[key]; isOpen && serviceResolves[recovery] == kind {
		delete(d.t.open, key)
		d.out = append(d.out, store.EventRow{Cluster: d.name, Node: key, Severity: "info", Kind: recovery, Message: msg})
	}
}

func (d *derivation) unhealthy(key, kind, sev, msg string) {
	delete(d.t.good, key)
	d.t.bad[key]++
	if d.t.bad[key] >= raiseAfter {
		d.raise(key, kind, sev, msg)
		return
	}
	d.present[key] = true
}

func (d *derivation) healthy(key, recovery, msg string) {
	delete(d.t.bad, key)
	d.present[key] = true
	if _, isOpen := d.t.open[key]; !isOpen {
		return
	}
	d.t.good[key]++
	if d.t.good[key] >= clearAfter {
		delete(d.t.good, key)
		d.ok(key, recovery, msg)
	}
}

func gate(ageSec int64) bool { return time.Duration(ageSec)*time.Second >= serviceAgeGate }

func (d *derivation) workloads(list []cluster.WorkloadHealth) {
	for _, w := range list {
		if d.skip[w.Namespace] || w.Kind == "Job" {
			continue
		}
		key := serviceKey(w.Kind, w.Namespace, w.Name)
		obj := fmt.Sprintf("%s %s/%s", w.Kind, w.Namespace, w.Name)
		if w.Ready < w.Desired && w.Desired > 0 && gate(w.AgeSec) {
			d.unhealthy(key, "workload.unavailable", "warn", fmt.Sprintf("%s has %d/%d replicas ready", obj, w.Ready, w.Desired))
			continue
		}
		d.healthy(key, "workload.available", fmt.Sprintf("%s is available again (%d/%d)", obj, w.Ready, w.Desired))
	}
}

func (d *derivation) pods(list []cluster.PodHealth) {
	for _, p := range list {
		if d.skip[p.Namespace] {
			continue
		}
		key := serviceKey("Pod", p.Namespace, p.Name)
		obj := fmt.Sprintf("pod %s/%s", p.Namespace, p.Name)
		hist := d.t.restarts[key]
		if len(hist) == 0 || hist[len(hist)-1].count != p.Restarts {
			hist = append(hist, restartSample{at: d.now, count: p.Restarts})
		}
		cut := 0
		for cut < len(hist)-1 && d.now.Sub(hist[cut].at) > crashloopWindow {
			cut++
		}
		hist = hist[cut:]
		d.t.restarts[key] = hist
		burst := p.Restarts-hist[0].count >= crashloopRestarts
		quiet := p.Restarts == 0 || d.now.Sub(hist[len(hist)-1].at) >= crashloopWindow
		switch {
		case p.Phase == "CrashLoopBackOff" && gate(p.AgeSec):
			d.raise(key, "pod.crashloop", "warn", fmt.Sprintf("%s is in CrashLoopBackOff (%d restarts)%s", obj, p.Restarts, onNode(p.Node)))
		case burst && gate(p.AgeSec):
			d.raise(key, "pod.crashloop", "warn", fmt.Sprintf("%s restarted %d times in %s%s", obj, p.Restarts-hist[0].count, crashloopWindow, onNode(p.Node)))
		case (p.Phase == "Running" || p.Phase == "Succeeded") && quiet:
			d.ok(key, "pod.recovered", fmt.Sprintf("%s is %s and has not restarted for %s", obj, p.Phase, crashloopWindow))
		default:
			d.present[key] = true
		}
	}
}

func (d *derivation) claims(list []cluster.ClaimHealth) {
	for _, c := range list {
		if d.skip[c.Namespace] {
			continue
		}
		key := serviceKey("PersistentVolumeClaim", c.Namespace, c.Name)
		if c.Phase == "Pending" && gate(c.AgeSec) {
			d.raise(key, "pvc.pending", "warn", fmt.Sprintf("PVC %s/%s has been Pending for %s: no StorageClass or provisioner bound it", c.Namespace, c.Name, (time.Duration(c.AgeSec)*time.Second).Truncate(time.Minute)))
		} else if c.Phase == "Bound" {
			d.ok(key, "pvc.bound", fmt.Sprintf("PVC %s/%s is Bound", c.Namespace, c.Name))
		} else {
			d.present[key] = true
		}
	}
}

func (d *derivation) services(list []cluster.ServiceRow) {
	for _, s := range list {
		if d.skip[s.Namespace] || !s.HasSelector || s.Type == "ExternalName" {
			continue
		}
		key := serviceKey("Service", s.Namespace, s.Name)
		if s.Endpoints == 0 && gate(s.AgeSec) {
			d.unhealthy(key, "service.no-endpoints", "warn", fmt.Sprintf("Service %s/%s has no ready endpoints: its selector matches no running pod", s.Namespace, s.Name))
		} else if s.Endpoints > 0 {
			d.healthy(key, "service.endpoints", fmt.Sprintf("Service %s/%s has %d ready endpoint(s)", s.Namespace, s.Name, s.Endpoints))
		} else {
			d.present[key] = true
		}
	}
}

func (d *derivation) ingresses(list []cluster.IngressHealth) {
	for _, i := range list {
		if d.skip[i.Namespace] {
			continue
		}
		key := serviceKey("Ingress", i.Namespace, i.Name)
		if !i.HasAddress && gate(i.AgeSec) {
			d.unhealthy(key, "ingress.no-address", "warn", fmt.Sprintf("Ingress %s/%s has no address: no ingress controller claimed it", i.Namespace, i.Name))
		} else if i.HasAddress {
			d.healthy(key, "ingress.address", fmt.Sprintf("Ingress %s/%s has an address", i.Namespace, i.Name))
		} else {
			d.present[key] = true
		}
	}
}

func (d *derivation) flux(list []cluster.FluxHealth) {
	for _, f := range list {
		if d.skip[f.Namespace] || f.Suspended {
			continue
		}
		key := serviceKey(f.Kind, f.Namespace, f.Name)
		obj := fmt.Sprintf("%s %s/%s", f.Kind, f.Namespace, f.Name)
		switch {
		case f.Ready == "False" && f.Reason == "DependencyNotReady":
			d.present[key] = true
		case f.Ready == "False":
			msg, _, _ := strings.Cut(f.Message, "\n")
			d.unhealthy(key, "flux.not-ready", "warn", fmt.Sprintf("%s is not ready: %s", obj, msg))
		case f.Ready == "True":
			d.healthy(key, "flux.ready", obj+" is ready again")
		default:
			d.present[key] = true
		}
	}
}

func (d *derivation) pool(p *cluster.PoolHealth) {
	key := "MetalLB/pool/" + p.Range
	if p.Allocated >= p.Total {
		d.raise(key, "lb.pool-exhausted", "critical", fmt.Sprintf("MetalLB pool %s is exhausted (%d/%d): new LoadBalancer services will stay Pending", p.Range, p.Allocated, p.Total))
	} else {
		d.ok(key, "lb.pool-free", fmt.Sprintf("MetalLB pool %s has free addresses (%d/%d used)", p.Range, p.Allocated, p.Total))
	}
}

func (d *derivation) sweep() {
	t := d.t
	for key, kind := range t.open {
		if d.present[key] {
			continue
		}
		delete(t.open, key)
		delete(t.bad, key)
		delete(t.good, key)
		delete(t.restarts, key)
		if rec := recoveryFor(kind); rec != "" {
			d.out = append(d.out, store.EventRow{Cluster: d.name, Node: key, Severity: "info", Kind: rec, Message: key + " was deleted"})
		}
	}
	for key := range t.restarts {
		if !d.present[key] {
			delete(t.restarts, key)
		}
	}
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
