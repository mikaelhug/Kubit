package watch

import (
	"testing"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/store"
)

func kinds(evs []store.EventRow) []string {
	var out []string
	for _, e := range evs {
		out = append(out, e.Kind)
	}
	return out
}

func hasKind(evs []store.EventRow, kind string) bool {
	for _, e := range evs {
		if e.Kind == kind {
			return true
		}
	}
	return false
}

func TestWorkloadUnavailableNeedsTwoCollectionsAndAge(t *testing.T) {
	tr := NewServiceTracker()
	now := time.Now()
	young := &cluster.ServiceHealth{Workloads: []cluster.WorkloadHealth{{Kind: "Deployment", Namespace: "app", Name: "web", Ready: 0, Desired: 3, AgeSec: 60}}}
	if evs := tr.Derive("c", young, now, nil); len(evs) != 0 {
		t.Fatalf("young workload alerted: %v", kinds(evs))
	}
	old := &cluster.ServiceHealth{Workloads: []cluster.WorkloadHealth{{Kind: "Deployment", Namespace: "app", Name: "web", Ready: 0, Desired: 3, AgeSec: 600}}}
	if evs := tr.Derive("c", old, now, nil); len(evs) != 0 {
		t.Fatalf("first short collection alerted: %v", kinds(evs))
	}
	evs := tr.Derive("c", old, now, nil)
	if !hasKind(evs, "workload.unavailable") {
		t.Fatalf("second short collection did not alert: %v", kinds(evs))
	}
	if evs[0].Node != "Deployment/app/web" || evs[0].Severity != "warn" {
		t.Fatalf("unexpected event %+v", evs[0])
	}
	// Steady state: no repeat.
	if evs := tr.Derive("c", old, now, nil); len(evs) != 0 {
		t.Fatalf("alert repeated: %v", kinds(evs))
	}
	fixed := &cluster.ServiceHealth{Workloads: []cluster.WorkloadHealth{{Kind: "Deployment", Namespace: "app", Name: "web", Ready: 3, Desired: 3, AgeSec: 900}}}
	for i := 1; i < clearAfter; i++ {
		if evs := tr.Derive("c", fixed, now, nil); len(evs) != 0 {
			t.Fatalf("recovery after %d healthy collection(s): %v", i, kinds(evs))
		}
	}
	evs = tr.Derive("c", fixed, now, nil)
	if !hasKind(evs, "workload.available") {
		t.Fatalf("recovery not reported: %v", kinds(evs))
	}
}

func TestFlappingServiceKeepsOneAlert(t *testing.T) {
	tr := NewServiceTracker()
	now := time.Now()
	at := func(endpoints int) *cluster.ServiceHealth {
		return &cluster.ServiceHealth{Services: []cluster.ServiceRow{{Namespace: "metallb-system", Name: "webhook", Type: "ClusterIP", HasSelector: true, Endpoints: endpoints, AgeSec: 900}}}
	}
	var all []string
	for _, e := range []int{0, 0, 1, 0, 1, 0, 1, 0} {
		all = append(all, kinds(tr.Derive("c", at(e), now, nil))...)
	}
	if len(all) != 1 || all[0] != "service.no-endpoints" {
		t.Fatalf("a flapping service must raise once and never recover mid-flap: %v", all)
	}
	all = nil
	for i := 0; i < clearAfter; i++ {
		all = append(all, kinds(tr.Derive("c", at(1), now, nil))...)
	}
	if len(all) != 1 || all[0] != "service.endpoints" {
		t.Fatalf("stable endpoints resolve once after %d collections: %v", clearAfter, all)
	}
}

func TestPodCrashloopByReasonAndByRestartBurst(t *testing.T) {
	tr := NewServiceTracker()
	t0 := time.Now()
	sh := func(phase string, restarts int32) *cluster.ServiceHealth {
		return &cluster.ServiceHealth{Pods: []cluster.PodHealth{{Namespace: "app", Name: "web-1", Phase: phase, Restarts: restarts, AgeSec: 900}}}
	}
	if evs := tr.Derive("c", sh("CrashLoopBackOff", 4), t0, nil); !hasKind(evs, "pod.crashloop") {
		t.Fatalf("CrashLoopBackOff not alerted: %v", kinds(evs))
	}
	// Briefly Running between back-offs is not a recovery.
	if evs := tr.Derive("c", sh("Running", 4), t0.Add(time.Minute), nil); len(evs) != 0 {
		t.Fatalf("flapped to recovered too early: %v", kinds(evs))
	}
	if evs := tr.Derive("c", sh("Running", 4), t0.Add(11*time.Minute), nil); !hasKind(evs, "pod.recovered") {
		t.Fatalf("quiet window did not recover: %v", kinds(evs))
	}
	// Restart burst without a waiting reason: +3 within the window.
	tr = NewServiceTracker()
	tr.Derive("c", sh("Running", 0), t0, nil)
	tr.Derive("c", sh("Running", 1), t0.Add(2*time.Minute), nil)
	if evs := tr.Derive("c", sh("Running", 2), t0.Add(4*time.Minute), nil); len(evs) != 0 {
		t.Fatalf("two restarts alerted: %v", kinds(evs))
	}
	if evs := tr.Derive("c", sh("Running", 3), t0.Add(6*time.Minute), nil); !hasKind(evs, "pod.crashloop") {
		t.Fatalf("restart burst not alerted: %v", kinds(evs))
	}
	// Samples older than the window drop out: no new alert after quiet time.
	tr = NewServiceTracker()
	tr.Derive("c", sh("Running", 0), t0, nil)
	if evs := tr.Derive("c", sh("Running", 3), t0.Add(20*time.Minute), nil); len(evs) != 0 {
		t.Fatalf("stale restart history alerted: %v", kinds(evs))
	}
	// A never-restarted pod is fine immediately.
	tr = NewServiceTracker()
	tr.Seed([]store.EventRow{{Node: "Pod/app/web-1", Kind: "pod.crashloop"}})
	if evs := tr.Derive("c", sh("Running", 0), t0, nil); !hasKind(evs, "pod.recovered") {
		t.Fatalf("zero-restart pod not recovered: %v", kinds(evs))
	}
}

func TestYoungCrashloopIsNotAlerted(t *testing.T) {
	tr := NewServiceTracker()
	sh := &cluster.ServiceHealth{Pods: []cluster.PodHealth{{Namespace: "kube-system", Name: "kube-controller-manager-cp-01", Phase: "CrashLoopBackOff", Restarts: 2, AgeSec: 90}}}
	if evs := tr.Derive("c", sh, time.Now(), nil); len(evs) != 0 {
		t.Fatalf("bootstrap-time crashloop alerted: %v", kinds(evs))
	}
}

func TestDeletedObjectResolvesAndIgnoreNamespaces(t *testing.T) {
	tr := NewServiceTracker()
	now := time.Now()
	pend := &cluster.ServiceHealth{Claims: []cluster.ClaimHealth{{Namespace: "app", Name: "data", Phase: "Pending", AgeSec: 900}, {Namespace: "ci", Name: "scratch", Phase: "Pending", AgeSec: 900}}}
	evs := tr.Derive("c", pend, now, []string{"ci"})
	if len(evs) != 1 || evs[0].Kind != "pvc.pending" || evs[0].Node != "PersistentVolumeClaim/app/data" {
		t.Fatalf("expected one pvc.pending for app/data, got %+v", evs)
	}
	evs = tr.Derive("c", &cluster.ServiceHealth{}, now, nil)
	if len(evs) != 1 || evs[0].Kind != "pvc.bound" {
		t.Fatalf("deletion should resolve with pvc.bound, got %+v", evs)
	}
}

func TestServicesIngressAndPool(t *testing.T) {
	tr := NewServiceTracker()
	now := time.Now()
	sh := &cluster.ServiceHealth{
		MetalLB:   true,
		Services:  []cluster.ServiceRow{{Namespace: "app", Name: "api", Type: "ClusterIP", HasSelector: true, Endpoints: 0, AgeSec: 900}, {Namespace: "app", Name: "ext", Type: "ExternalName", HasSelector: false, Endpoints: 0, AgeSec: 900}},
		Ingresses: []cluster.IngressHealth{{Namespace: "app", Name: "web", HasAddress: false, AgeSec: 900}},
		Pool:      &cluster.PoolHealth{Range: "10.0.0.200-10.0.0.201", Total: 2, Allocated: 2},
	}
	evs := tr.Derive("c", sh, now, nil)
	if got := kinds(evs); len(evs) != 1 || evs[0].Kind != "lb.pool-exhausted" {
		t.Fatalf("first collection: only the pool alerts at once: %v", got)
	}
	evs = tr.Derive("c", sh, now, nil)
	got := kinds(evs)
	for _, want := range []string{"service.no-endpoints", "ingress.no-address"} {
		if !hasKind(evs, want) {
			t.Fatalf("missing %s in %v", want, got)
		}
	}
	if len(evs) != 2 {
		t.Fatalf("unexpected extra events: %v", got)
	}
	sh.Services[0].Endpoints = 2
	sh.Ingresses[0].HasAddress = true
	sh.Pool.Allocated = 1
	evs = tr.Derive("c", sh, now, nil)
	if got := kinds(evs); len(evs) != 1 || evs[0].Kind != "lb.pool-free" {
		t.Fatalf("the pool resolves at once, the rest wait for stability: %v", got)
	}
	for i := 1; i < clearAfter; i++ {
		evs = tr.Derive("c", sh, now, nil)
	}
	for _, want := range []string{"service.endpoints", "ingress.address"} {
		if !hasKind(evs, want) {
			t.Fatalf("missing recovery %s in %v", want, kinds(evs))
		}
	}
}

func TestSeedPreventsReraise(t *testing.T) {
	tr := NewServiceTracker()
	tr.Seed([]store.EventRow{{Node: "Deployment/app/web", Kind: "workload.unavailable"}})
	old := &cluster.ServiceHealth{Workloads: []cluster.WorkloadHealth{{Kind: "Deployment", Namespace: "app", Name: "web", Ready: 0, Desired: 3, AgeSec: 600}}}
	tr.Derive("c", old, time.Now(), nil)
	if evs := tr.Derive("c", old, time.Now(), nil); len(evs) != 0 {
		t.Fatalf("seeded alert re-raised: %v", kinds(evs))
	}
}

func TestFluxNotReadyRaisesAndClears(t *testing.T) {
	tr := NewServiceTracker()
	now := time.Now()
	broken := &cluster.ServiceHealth{Flux: []cluster.FluxHealth{{Kind: "Kustomization", Namespace: "flux-system", Name: "flux-system", Ready: "False", Message: "Deployment/shop/shop dry-run failed: .spec.replicas: expected numeric\nNamespace/shop created"}}}
	if evs := tr.Derive("c", broken, now, nil); len(evs) != 0 {
		t.Fatalf("first failed collection alerted: %v", kinds(evs))
	}
	evs := tr.Derive("c", broken, now, nil)
	if len(evs) != 1 || evs[0].Kind != "flux.not-ready" || evs[0].Message != "Kustomization flux-system/flux-system is not ready: Deployment/shop/shop dry-run failed: .spec.replicas: expected numeric" {
		t.Fatalf("second failed collection: %+v", evs)
	}
	suspended := &cluster.ServiceHealth{Flux: []cluster.FluxHealth{{Kind: "HelmRelease", Namespace: "a", Name: "b", Ready: "False", Suspended: true}}}
	if evs := NewServiceTracker().Derive("c", suspended, now, nil); len(evs) != 0 {
		t.Fatalf("suspended object alerted: %v", kinds(evs))
	}
	fixed := &cluster.ServiceHealth{Flux: []cluster.FluxHealth{{Kind: "Kustomization", Namespace: "flux-system", Name: "flux-system", Ready: "True"}}}
	for i := 0; i < clearAfter-1; i++ {
		if evs := tr.Derive("c", fixed, now, nil); len(evs) != 0 {
			t.Fatalf("cleared too early: %v", kinds(evs))
		}
	}
	if evs := tr.Derive("c", fixed, now, nil); !hasKind(evs, "flux.ready") {
		t.Fatalf("recovery: %v", kinds(evs))
	}
}
