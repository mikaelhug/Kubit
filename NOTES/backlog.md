# Backlog

## Verify on real clusters
- `kubit apply` end to end: create, add, config, Talos and Kubernetes upgrades, removal,
  platform; the Lease lock and alert quieting; encrypted state on the real platform module.
- `kubit export lab --repo` on the live `lab`, then a plan with no changes.
- talos-backup against a real bucket; a restore from one of its snapshots.
- Lifecycle/Image API upgrade: installer pulled into `NS_SYSTEM`, digest-pinned
  `ImageName` accepted, plain reboot (and kexec) boots the new slot.
- Host firewall: `nftableschains`, `kubectl top nodes`, a MetalLB Service, a node add
  from a second subnet.
- Disk encryption, TPM and watchdog detection on hardware.
- Longhorn on data disks: shared mount propagation of `/var/mnt/data-N`.
- Pushed stage on a reboot/upgrade; no stale pushes after sleep.
- SMTP STARTTLS and implicit TLS against a real provider; `master.key` fallback on Linux.
- QEMU lab (`hack/qemu`) has never booted a VM.

## Converge
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
- Node removal leaves its subnet in the others' firewall rules until the next apply.
- Node add's `firewall` step applies each node's full config, so one unreachable node
  fails the add; apply only the firewall documents.
- Turning `network.policies` off leaves the kube-network-policies DaemonSet running;
  prune it in the manifests step.
- Changing `platform.flux.repository.path` prunes everything under the old path before
  the new Kustomizations re-create it; the plan should warn.
- The tofu provider lock file lives in `~/.kubit`; writing `.terraform.lock.hcl` into the
  repo would pin providers.
- A node declared at its DHCP lease is found by MAC only before it joins; afterwards the
  plan asks for a cluster.yaml update or a static address.
- Platform apply: retry on transient API errors, scale the Helm timeout with node count,
  surface pod events while a Deployment never rolls out.
- A failed plan/apply clears `ingress_ip`, which raises `lb.lost`; keep outputs on error.
- Containerd config v4 arrives with Talos 1.15; check `RegistryMirrorConfig` first.
- `configBehind` relies on byte-identical `config.Generate`; add a test.
- No linter catches deprecated machinery calls; run `staticcheck` (SA1019).

## Platform
- Gateway API objects are not in the Network view and raise no alerts.
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
- Info-level events accumulate unacked; auto-ack after a day.
- Workload alert thresholds are fixed; make them per-cluster if needed.
- `status` asks only the first reachable control plane for the etcd leader.
- `ConfigStatus` regenerates every node config on each fetch.
- A pushed stage and a tick can publish out of order; stamp statuses with probe time.
- A node that refuses the COSI stage watch logs once per tick.
- The status-bar uptime keeps ticking while disconnected.
- Secrets editor: keys under sequences are not editable; non-Secret files split keys on
  dots; encrypted comments are dropped on a write.
- Security: no CSRF guard on bodiless POSTs while loopback needs no token; check
  `Origin`/`Sec-Fetch-Site` in `ServeHTTP`. WebSocket origin check fails behind a proxy
  that rewrites `Host`.
- `platform.argocd` and `platform.ingressNginx` are still read and folded in on load.

## Dev harness
- vfkit console: direct-kernel boot with `console=hvc0` would give a readable log.
- vmnet NAT is flaky for ~2 min after VM boot; the harness could wait for :50000.
