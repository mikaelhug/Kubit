# Backlog

## Verify on real clusters
- `kubit apply` end to end: create, add, config, Talos and Kubernetes upgrades, removal,
  platform; the Lease lock and alert quieting; encrypted state on the real platform module.
- `kubit export lab --repo` with a `dbeba6f` build on the live `lab`, then a plan with no
  changes from the current build.
- Create resume from live state: a create stopped after install, then `kubit apply`.
- Layout guard: a plan against a node whose EPHEMERAL/STATE volume config differs.
- talos-backup against a real bucket; a restore from one of its snapshots.
- Lifecycle/Image API upgrade: installer pulled into `NS_SYSTEM`, digest-pinned
  `ImageName` accepted, plain reboot (and kexec) boots the new slot.
- Longhorn on data disks: shared mount propagation of `/var/mnt/data-N`.
- Webhook: one POST per raise and per resolve; none for faults present at start.
- QEMU lab (`hack/qemu`) has never booted a VM.
- Console create: *Add* on a maintenance machine to a new repo dir, then *Apply* creates
  the cluster.
- Address moves on `lab` (each needs a go): pin cp-01 static at .240; move a worker; move
  cp-01 with the endpoint; a wrong gateway to prove the 3-minute try rollback. Proven on VMs
  by `hack/scenario.sh` (pin, move a worker, move the control plane with the endpoint and its
  etcd peer URL, release to DHCP); still unproven: the try-mode rollback.

## Converge
- `ApplyConfigs` (internal/cluster/apply.go) has an empty `if wantKubelet == "" {}` block.
- An endpoint change alone (a new VIP or DNS name with no node moving) is refused; it needs
  every node re-configured and kubeconfigs rotated in one step.
- Address moves run one node at a time; a big worker pool takes a reboot each.
- Node temperatures are shown, not alerted on; a node at its chip's critical limit could
  raise an alert.
- Console apply has no cancel; stopping the daemon is the only way out of a running apply.
- Console edits cover nodes, addresses, add-on toggles, versions and secrets; roles,
  labels, patches and add-on values are edited in the cluster.yaml editor.
- Machine page Services and Logs tabs each fetch the service list when shown.
- A declared cluster's page shows Changes, Secrets and Repository only, so a wrongly added
  node is removed in the cluster.yaml editor; a Nodes view for declared clusters would allow
  *Remove*.
- A declared (not yet installed) node whose DHCP lease moved is still a plan problem; the
  console could offer to write the new address.
- `kubit pxe <repos>` sends every declared MAC to its local disk, also before it is
  installed; a declared machine rebooted before *Apply* then misses maintenance mode.
- A failed node stops `kubit apply`; a rerun re-applies every node first. Resume at the
  failed node.
- The manifests step fails when the Kubernetes API does not answer although every node
  config was applied, and the platform step is skipped; report it as a warning.
- A Talos upgrade that fails mid-run leaves upgraded nodes ahead of the others; the next
  plan shows a config diff until the upgrade finishes. Say so in the plan.
- The etcd check before a control plane goes down reads Talos's service health (20 s
  refresh); add a direct quorum read if that window matters.
- A node left cordoned by a failed upgrade blocks `upgrade kubernetes`; release Kubit's
  cordon in `ApplyConfigs` too.
- Drain timeout (5 min) and PDB policy are fixed; Longhorn's last-replica PDB can block.
- A NotReady node is still drained and waits out the full timeout; skip the drain when the
  node is NotReady.
- A node whose clock lags more than a day rejects Kubit's certificates ("expired
  certificate"); the plan could read Talos's time status and report the skew as a problem.
- Turning `network.policies` off leaves the kube-network-policies DaemonSet running;
  prune it in the manifests step.
- Changing `platform.flux.repository.path` prunes everything under the old path before
  the new Kustomizations re-create it; the plan should warn.
- The tofu provider lock file lives in `~/.kubit`; writing `.terraform.lock.hcl` into the
  repo would pin providers.
- A node declared at its DHCP lease is found by MAC (a /24 scan) only before it joins;
  afterwards the plan asks for a cluster.yaml update or a static address.
- Platform apply: retry on transient API errors, scale the Helm timeout with node count,
  surface pod events while a Deployment never rolls out.
- Containerd config v4 arrives with Talos 1.15; check `RegistryMirrorConfig` first.
- No linter catches deprecated machinery calls; run `staticcheck` (SA1019).

## Platform
- The `nginx` IngressClass shim (traefik.tf `traefik_nginx_class`, the `kubernetesIngressNGINX`
  provider, flux.tf depends_on) and the `ingress_nginx` plan mapping can go once `lab` has no
  Ingress with `ingressClassName: nginx`.
- The unencrypted-state fallback in `tofu/runner.go` can go once `lab`'s repo state is
  confirmed encrypted.
- Gateway API: Network lists HTTPRoutes but not the Gateways themselves; none raise alerts.
- Traefik's own CRDs are never upgraded by Helm; a chart bump needs a CRD apply step.
- Gateway API CRDs owned by another manager fight with Kubit's force-conflicts apply.
- The default Gateway listens on HTTP only; offer a cert-manager HTTPS listener.
- The empty `ingress-nginx` namespace stays after the Traefik migration.
- cert-manager has no resource requests.
- Add-on readiness for metrics-server counts all of kube-system.
- Kubelet serving-certificate rotation would let metrics-server drop
  `--kubelet-insecure-tls`.
- Builds: rootless BuildKit, auth on buildkitd and the registry, registry GC, private
  repositories.
- Flux: *Sync now*, a push webhook Receiver, multi-tenancy lockdown, generated per-app
  Kustomizations, grouping on the Flux card.
- Platform secrets in add-on values are plaintext in cluster.yaml and tfvars.
- Cilium as a create-time CNI option.

## Observer and console
- `status` asks only the first reachable control plane for the etcd leader.
- Removing a served repo leaves its cluster in the daemon until restart.
- The status-bar uptime keeps ticking while disconnected.
- Secrets: keys under sequences are not editable; non-Secret files split keys on dots;
  encrypted comments are dropped on a write.
- Secrets attribution follows only the root `flux-system` Kustomization: Flux Kustomization
  objects in the repo with their own `path`, `.sourceignore` and `patches`/`components` are
  not followed. A file under two clusters' Flux paths is shown for the first one.
- *Let Flux decrypt* on a shared apps repo adds one cluster's key at a time; a repo synced by
  several clusters needs it once per cluster.
- Security: no CSRF guard on bodiless POSTs while loopback needs no token; check
  `Origin`/`Sec-Fetch-Site` in `ServeHTTP`. WebSocket origin check fails behind a proxy
  that rewrites `Host`.

## Dev harness
- vfkit console: direct-kernel boot with `console=hvc0` would give a readable log.
- vmnet NAT is flaky for ~2 min after VM boot; the harness could wait for :50000.
- VM clocks drift after the Mac sleeps (minutes); `vm.sh stop n` and `start n --no-iso`
  resets them.
