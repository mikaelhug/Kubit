# Kubit reliability plan — one click to a ready cluster

Goal: a user selects machines (or a lab host), clicks once, and is handed a cluster that
is actually usable — nodes Ready, add-ons healthy, a LoadBalancer service gets an IP — or
a clear, early "this won't fit / fix X" instead of a 30-minute failure. This plan is
grounded in the failures actually hit during bring-up, most recently a cluster whose
control plane and nodes came up but whose add-ons OOM-crashlooped and left tofu wedged.

## Root causes observed (what the plan must prevent)

1. **Undersized nodes OOM the add-ons.** 1 GiB workers have ~450 MiB *allocatable* after
   Talos/kubelet reservations; `kubectl top` showed workers at 138–147% and the control
   plane (1.9 GiB) at 91%. metallb speaker/frr-k8s crashlooped, one worker fell out of the
   cluster, and Helm never went ready. The provision "succeeded" but the cluster was not
   usable.
2. **A transient API blip wedges tofu.** op 139's `helm install metallb` created the
   release, then the apiserver briefly restarted under memory pressure; tofu failed on
   `connection refused` before recording the release, so re-apply hit "cannot re-use a
   name that is still in use" — a stuck state needing manual cleanup.
3. **Wasteful add-on defaults.** metallb runs the heavy FRR (BGP) stack even though Kubit
   only configures L2Advertisement; add-ons ship with no resource requests, so the
   scheduler overcommits a small node into OOM churn instead of a clean Pending.
4. **Noise, not signal.** A failure surfaced as many error popups rather than one banner
   with the cause and a Retry.

## Principles

- **Fail fast, before destructive work.** Check capacity/links up front; never wipe a disk
  or half-install add-ons for a plan that cannot succeed.
- **Every long op is idempotent and converges on retry.** Re-running create/apply always
  moves forward; a transient blip is recovered, not fatal.
- **Right-size by default.** Defaults produce a working cluster; the UI blocks a plan the
  cluster can't run.
- **One status, one action.** A phase fails with a single cause + a Retry, not a stream of
  toasts. "Ready" means usable, verified by a smoke test.

## Workstreams (priority order)

### P0 — Capacity & sizing (prevents the OOM class — the actual failure) — DONE 2026-09-18
(2 GiB floor for every VM, `planFor` host-fitting defaults, `worker-undersized` lint + preflight when add-ons are on, `node.memory-small` alert.)

- Realistic lab defaults: **worker ≥ 2 GiB (default 3 GiB), control plane ≥ 3 GiB** when
  the platform stack is enabled; keep the current 2 GiB CP floor only for a bare cluster.
  Files: `web/src/components/LabHost.tsx` (`defaultVM`, `MIN_CP_MIB`, `rowsProblem`),
  `internal/api/labhost.go` (`validate`, `sizes`, `minControlPlaneMiB`).
- Preflight a **fits-the-workload** check in `internal/cluster/create.go:preflight`: for
  each node, require *allocatable*-equivalent RAM ≥ a role+add-on floor (control plane
  base + etcd + apiserver; worker base + enabled add-ons' requests). Block with a clear
  message naming the node and the shortfall, before any config is applied. Base the floor
  on the ~450 MiB/GiB allocatable ratio observed, not nominal RAM.
- Lab plan: keep the host-memory reserve check (done) and add a per-node *usable* estimate
  so the dialog warns "these workers are too small for the platform stack" at plan time.

### P0 — Platform apply robustness (prevents the wedged-tofu class) — `atomic` DONE; retry/timeout scaling open

- **`atomic = true` on every `helm_release`** (`internal/tofu/templates/platform/*.tf`): a
  failed install auto-uninstalls, so there is never an orphan release blocking re-apply.
  This alone fixes "cannot re-use a name".
- **Retry the whole apply on transient/API-unreachable errors** in the platform-apply path
  (`internal/cluster` ApplyPlatform + `internal/tofu`): bounded backoff on
  `connection refused` / `Kubernetes cluster unreachable`; wait for the API to be stable
  before starting tofu.
- **Scale Helm `timeout` with node count**; `wait=true timeout=600` is fragile on slow
  small clusters.
- Make **Retry** a first-class, always-converging action on a failed platform op (reuse
  the existing operation retry).

### P1 — Add-on right-sizing & correctness — DONE 2026-09-18 (FRR off, requests on metallb/ingress/metrics-server, single ingress replica)

- **metallb: disable FRR** (`frrk8s.enabled=false`, chart 0.16.1) unless BGP is configured
  — only L2Advertisement is used. Merge as a default in `internal/tofu/render.go`
  (`metallbVars.Values`), overridable via `platform.metallb.values`. Verify the exact key
  against the chart before shipping.
- **Resource requests/limits on every add-on** so the scheduler leaves a node Pending
  (visible, recoverable) instead of OOM-churning: metallb, ingress-nginx, metrics-server,
  argocd, cert-manager. Small, sane defaults suited to a homelab.
- ingress-nginx: single replica on small clusters; address the admission-webhook patch-job
  flake already in the backlog (disable the patch job with cert-manager-issued certs, or
  retry).

### P1 — Cluster bring-up resilience (already partly done; finish it)

- Kept from the provisioning review: disk-boot before apply + verified switch, control-plane
  RAM floor (now on *usable* RAM), `WaitForReboot` maintenance-mode fast-fail,
  `sameNodes` by identity, Ready-timeout leaves the cluster `Bootstrapped` not `Failed`.
- **Detect a node that never registers** (one worker silently dropped): the Ready wait must
  report "node X never joined" distinctly from "NotReady", and the flow must not proceed to
  the platform with a missing node. Files: `internal/k8s/client.go:WaitReady`,
  `internal/cluster/create.go:waitReady`.
- Scale the Ready timeout with node count / first-image-pull time.

### P2 — UX: one clear status, no popup spam — replay/flap fixes and `degraded` state DONE; operation banner + smoke test open

- Collapse per-error event toasts into the operation's own error banner: an operation
  failure is one banner with the cause + Retry, not N popups. Files: `web/src/store.ts`,
  `web/src/live.ts`, the toast plumbing, `ActivityDrawer`.
- Failure "what to do" runbooks for the common provision/cluster/platform failures (Kubit
  already has this pattern for alerts — `web/src/runbooks.ts`).
- A cluster is only shown **ready** when nodes are Ready **and** add-ons are healthy; a
  degraded cluster shows a clear state with a one-click reconcile.

### P2 — Prove "ready means usable" (smoke test)

- At the end of create, deploy a tiny workload + a `LoadBalancer` Service and assert it
  schedules, becomes Ready, and gets an IP from MetalLB. Only then report the cluster ready.
  This would have caught the OOM/metallb failure as a create-time failure, not a surprise.

### P3 — Keep it fixed (the harness as a gate)

- Make `hack/lab/lab.sh` the end-to-end gate: provision → VMs → cluster → platform → argocd,
  asserting every add-on healthy and the smoke-test workload up. Run it as a `make`
  target / in CI before shipping.
- Size the harness VMs realistically (≥ 2 GiB workers) so the gate reflects reality.
- Golden-config tests for the tofu render (FRR off, resource limits present).

## ArgoCD

ArgoCD is heavy and only makes sense once the sizing work lands (it needs headroom). Give
it the same treatment: `atomic` install, resource requests, retry, and a post-install
health assertion in the smoke test. Do not enable it by default on a small lab.

## Sequencing

1. P0 sizing (defaults + preflight fits-check) and P0 `atomic=true` + apply-retry — these
   two unblock a working cluster and stop the two failure classes seen.
2. P1 metallb FRR off + add-on resource limits.
3. P2 smoke test + status/UX consolidation.
4. P3 harness-in-CI, golden tests.

Verification for every change: `go test ./...`, `tofu` golden-config diff, and a full
`hack/lab/lab.sh` run ending in a green smoke test; then the EliteDesk end to end.
