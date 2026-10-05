# Backlog

- Forget cluster leaves `~/.kubit/clusters/<name>/` behind (admin `kubeconfig`,
  `talosconfig`, `snapshots/`, `infra/`), although the dialog says it deletes the
  talosconfig and kubeconfig. Seen 2026-09-28 after forgetting `lab`. Decide: delete the
  directory (keep snapshots only if the user asks), and make the dialog match.
- vfkit console: arm64 ISO logs to ttyAMA0; direct-kernel boot with `console=hvc0` would
  give a readable `console.log`.
- vmnet NAT is flaky for ~2 min after VM boot; harness could poll :50000 before returning.
- `status` etcd leader detection only asks the first reachable control plane; a
  cluster whose first CP is down reports no leader.
- Stale mirror pods (`Pending`, old image) linger in kube-system for a while after a
  Kubernetes upgrade; harmless, but the dashboard could surface component versions
  from the API server rather than pod images.
- ingress-nginx Helm upgrades occasionally fail on the `ingress-nginx-admission-patch`
  post-upgrade hook (BackoffLimitExceeded); the retry succeeded. Consider disabling the
  patch job (`controller.admissionWebhooks.patch.enabled=false` with cert-manager
  issued webhook certs) or an automatic single retry of failed platform applies.
- Add-on readiness for metrics-server counts all of kube-system; scope by the release's
  label selector instead.
- SMTP forwarding verified only in plain mode against a local receiver; STARTTLS and
  implicit-TLS paths need a run against a real provider (Gmail/Fastmail app password).
- The `master.key` fallback has unit tests but no run on a real Linux host yet.
- Workload alert rules are fixed (5 min age gate, 3 restarts / 10 min); make them
  per-cluster settings if a workload legitimately restarts often (e.g. batch jobs).
- Info-level events (node.ready, api.back, …) accumulate unacked in the events table;
  they are hidden from the alert list but the count grows. Auto-ack info events
  older than a day.
- Restore of a control plane whose `ip:` has changed since the snapshot was taken is
  not handled (snapshot carries the old Node objects; kubelets re-register, but the
  old Node entries linger until deleted).
- Maintenance window: the daemon's own scheduled snapshots ignore it on purpose
  (non-disruptive); consider a per-cluster switch for "quiet hours" on alerts.
- Kubit's old default :8080 collided with another local app during M9 testing; the
  default is now :8090. Consider a free-port fallback with a clear log line, or making
  the port part of Kubit settings.
- The live ring buffer (2000 messages) is per daemon process; a long outage still ends
  in a resync, which is correct but reloads everything. Fine at this scale.
- Storage consumer for data disks: opt-in `localPath` platform add-on (rancher local-path-provisioner with `nodePathMap` → `/var/mnt/data-*`) so PVCs land on the data volumes without hostPath; Longhorn/OpenEBS stay user-deployed.
- Data-disk selectors by serial/WWN instead of dev_path (Talos `disk.serial`): dev paths can shift on multi-controller boxes; inventory would need the serial first.
- Boot into Talos and the lab install share `labWaitBoot`; the same phase machine
  could serve `discover` when it waits for a PXE-booted machine.
- `writeErr` now maps gRPC Unavailable/DeadlineExceeded to 502/504 (was 500); scripts
  matching on 500 would notice.
- QEMU lab (`hack/qemu`): written on macOS and has not booted a VM yet (OVMF path,
  tap ownership, dnsmasq lease file permissions are the likely first fixes). GitHub
  Actions removed; signed releases would need a new home if ever wanted.
- Longhorn: unverified on a cluster. Check that Talos propagates `/var/mnt/data-N`
  user volumes into the kubelet with shared propagation (needed for Longhorn's
  bind mounts); if not, a UserVolumeConfig-based `longhorn` volume or a v1alpha1
  kubelet mount fallback is needed. Add a Storage-tab panel reading
  `longhorn.io/v1beta2` volumes (health, replicas, backup target).
- Platform apply: retry the whole tofu apply with backoff on transient API errors and
  scale the Helm timeout with node count (reliability plan P0, still open).
- Smoke test at the end of create: a tiny Deployment + LoadBalancer Service must get an
  IP before the cluster is reported ready (reliability plan P2).
- cert-manager still ships without resource requests; give it the same treatment
  before enabling by default (Flux's chart sets 100m/64Mi per controller).
- Observer (2026-09-19): Kubit lives on a laptop by design; sleep is handled (gaps
  re-baseline, `backup.stale` skips slept time). Open: the WebSocket reconnect after a
  long sleep always ends in a `resync` (fine); a per-machine "last contact" for
  maintenance-mode rows comes from discovery's `lastSeen` and is not labelled as such;
  the macOS per-process LAN denial seen on 2026-09-19 (an orphaned `kubit serve` got
  `EHOSTUNREACH` for every LAN address while other processes did not) is detected and
  reported but its cause is not proven.
- Namespace management (create/delete an app namespace with a Pod Security level) is left
  out on purpose: app namespaces live in Git with the app. Revisit only if Kubit ever
  deploys apps itself. The node page's pod table still lists every namespace (platform pods
  matter there); a scope toggle could follow if it gets long.
- App secrets (M23) follow-ups:
  - External Secrets Operator add-on with provider presets (Bitwarden Secrets Manager,
    Doppler, Infisical); Kubit keeps the provider's bootstrap token sealed and installs
    it like the SOPS key. For secrets that must come from a manager, not Git.
  - Key rotation in the UI: keep old and new identities in `keys.txt` during the
    rollover, show both recipients, drop the old one on confirm.
  - Private and SSH apps repositories: a deploy key Kubit generates, seals and installs
    as the GitRepository's `secretRef`.
  - Platform secrets in add-on values (ACME DNS tokens, Longhorn S3 credentials) are
    plaintext in cluster.yaml, readable by viewers and copied into tfvars/tfstate.
    Needs sealed values with viewer redaction.
  - tfvars and tfstate are plaintext on disk (0600, inside `~/.kubit`; sealed only in
    backups).
  - The Flux card shows the recipient Kubit holds, not whether the cluster's
    `flux-system/sops-age` still matches it; a drift check could compare the two.
- Builds follow-ups: rootless BuildKit (needs `user.max_user_namespaces` > 0 on the
  nodes), TLS and auth for buildkitd and the registry (any pod can push), registry garbage collection of old tags, private Git repositories (a deploy
  key), build logs streamed in the UI instead of the raw log link, rebuilds on source
  changes without a version bump.
- Platform apply: a Deployment that never rolls out (the registry on the first M25
  run) blocks `tofu apply` for 10 minutes with no progress line; Kubit could surface
  the pod's events while it waits.
- Storage on the system disk: Kubit does not detect an existing node whose EPHEMERAL
  already fills the disk (the `data-system` volume then stays unprovisioned); the node
  page could show the live volume status.
- Flux (M24) follow-ups:
  - *Sync now* on the Flux card: annotate the GitRepository with
    `reconcile.fluxcd.io/requestedAt` instead of waiting for the interval.
  - Push webhook: a notification-controller Receiver behind ingress, its token sealed
    by Kubit.
  - Multi-tenancy lockdown: the root Kustomization applies with cluster-admin, so the
    repository can touch platform namespaces.
    A service account with namespace-scoped rights, or the chart's `multitenancy`.
  - Per-app Kustomizations need a hand-written `flux/<app>.yaml` next to each folder;
    a generator (Flux Operator ResourceSet, or Kubit templating one per folder)
    would drop the boilerplate.
  - Changing `repository.path` prunes everything under the old path before the new
    Kustomizations re-create it (volumes lost). The Configure dialog should warn, or
    the root Kustomization could be switched with pruning suspended for one apply.
  - The Flux card lists every object; with many apps it wants grouping per app or
    hiding Ready rows.
- Node add's `firewall` step applies each existing node's full regenerated config, so a
  pending declaration change (an unapplied patch) reaches them too, and one unreachable
  node fails the add. Apply only the firewall documents, merged with the running config.
- `CheckCluster` runs on every edit, so a cluster whose generation already fails for an
  unrelated reason refuses every save with 422 until fixed. Watch clusters stored by
  older versions; offer a YAML save that skips the check for repairs.
- Host firewall unverified on Talos: `talosctl get nftableschains`, `kubectl top nodes`
  (pod→kubelet), a LoadBalancer Service (MetalLB memberlist) and a node add from a
  second subnet on a lab cluster with the firewall on.
- `POST /clusters/{name}/nodes` does not fill `tpm`/`watchdog` from inventory when the
  body omits them (dialog and CLI do). Fill from the machine row like the CLI.
- Declarations with `encryption: tpm` and a node without `tpm: true` no longer parse;
  only possible for declarations saved by an interim build before TPM validation landed.
  Intended; remember it if a stored cluster will not load.
- Existing nodes have no `watchdog`/`tpm` keys. A "refresh hardware flags from
  inventory" action could fill `tpm`/`watchdog`/`kvm` on declared nodes.
- Disk encryption, TPM and watchdog detection unverified on hardware: confirm `LS` sees
  `/dev/tpmrm0`, `/sys/firmware/efi`, `/dev/watchdog0` in maintenance mode (any error
  but NotFound counts as present), `volumestatus` shows luks2, `nodeID` volumes unlock
  after reboot, `watchdogtimerstatus` is armed and a hung node resets.
- Secure Boot installs are the prerequisite for making `tpm` the default: Talos 1.14
  sealing reads the PCR signing key only signed UKIs carry. Needs signed images from the
  Image Factory, key enrolment on the machines, and a PXE path that boots signed UKIs.
- `ConfigStatus` regenerates every node's config on each fetch (per cluster row write
  while a view is open). Cache by spec hash plus machine-config write if it shows up.
- Apply's manifests step reads bootstrap manifests right after the last node; if Talos
  has not re-rendered them yet the old ones are synced and NetworkPolicy enforcement
  waits for the next Apply. Lab check: turn `policies` on, Apply, look for the
  `kube-network-policies` DaemonSet; wait for the manifest version if it lags.
- `cluster.apply` stops before the manifests step when a node fails; a retry re-applies
  every node first. Resume at the failed node instead.
- `kubit status` does not print which nodes are behind the declaration. Add a line from
  `ConfigStatus`.
- `node.upgrade` now stores the config it applies but is not in `configKinds`
  (`internal/api/live.go`), so the `config` scope does not refresh after a single-node
  upgrade. Add it.
- Turning `network.policies` off stops Talos from rendering kube-network-policies, but
  the DaemonSet already in the cluster keeps enforcing. Prune it in the manifests step
  (server-side apply does not delete) when policies go off.
- Failed drain after a Talos install leaves the new image in the inactive slot; the node
  boots it on any later reboot. Show "installed, not booted" in Lifecycle (boot entry vs
  `readNodeImage`) or offer *Reboot without drain* from the failure.
- A node left cordoned by a failed upgrade looks like an operator's cordon, so a retry
  leaves it cordoned and the cluster precheck refuses it. Mark Kubit's cordon with an
  annotation and uncordon only those on retry.
- Drain timeout (5 min) and PDB policy are hardcoded; single-node clusters and Longhorn's
  last-replica PDB can block a drain. Offer *skip drain* / *disable eviction* per run.
- `LifecycleService.Upgrade` lacks the etcd guard the legacy `MachineService.Upgrade`
  (force=false) ran on each control plane; precheck and `waitBack` cover the run
  boundaries but not a member that turns unhealthy mid-run. Check etcd health before
  each control plane's drain.
- `configBehind` relies on byte-identical `config.Generate` output. Add a unit test that
  two Generate calls with the same bundle are equal so upgrades never flap "behind".
- No linter catches deprecated machinery calls; run `staticcheck` (SA1019).
- Lifecycle/Image API upgrade unverified on a node: installer pulled into `NS_SYSTEM`,
  the digest-pinned name accepted as `ImageName`, a plain reboot (and kexec) boots the
  new slot, the pre-reboot config apply on the old OS, install log volume.
- Containerd config v4 arrives with Talos 1.15; check Kubit's `RegistryMirrorConfig` and
  any containerd patches against it before allowing 1.15 targets.
- Pushed stage: a tick whose probe started before a pushed change stores the older
  stage, restarts that watch, and the fresh watch patches it back (one extra
  `status`/`nodes` pair). Have the tick consult the watch's last-seen time first.
- A node reachable on :50000 that refuses the COSI stage watch logs once per tick. Add a
  per-node backoff or log-once.
- Pushed stage unverified on a node: a reboot/upgrade on the lab should move the stage
  pill without waiting for a tick, and a sleep/wake should log no stale pushes.
- Gateway API objects (Gateway, HTTPRoute, GRPCRoute) are not in the Network view and
  raise no alerts (`ingress.no-address` covers Ingress only). List them, and alert on a
  route no Gateway accepts or a Gateway without an address.
- The empty `ingress-nginx` namespace stays after migration (Helm keeps namespaces). Add
  an explicit, reviewed cleanup step.
- Traefik's own CRDs come from the chart's `crds/` and are never upgraded by Helm. A
  chart bump that changes them needs a CRD apply step like the Gateway API CRDs.
- Gateway API CRDs owned by another manager (Flux, another controller) fight with
  Kubit's force-conflicts apply. Add a setting to skip Kubit's CRDs.
- The plan view shows full CRD bodies (`yaml_body_parsed`) on the first apply; fold or
  summarise CRD resources.
- The default Gateway listens on HTTP only; HTTPS listeners need `certificateRefs`
  through values. Offer a cert-manager-backed HTTPS listener when cert-manager is on.
- A failed plan/apply clears `ingress_ip` in platform status, which raises `lb.lost`
  while the address is in use. Keep the outputs on error (and drop the tfvars pin
  fallback).
- `metrics-server.tf` is not `tofu fmt` clean (alignment of `=`). Run `tofu fmt`.
- Cilium as a create-time CNI option (Flannel + kube-network-policies stays the
  default); needs `cni: none`, a Cilium chart in the platform layer before nodes go
  Ready, and kube-proxy replacement as a choice.
- Kubelet serving-certificate rotation (`rotate-server-certificates`) would let
  metrics-server drop `--kubelet-insecure-tls`; it needs an in-cluster CSR approver.
- A pushed stage `OnStatus` and a tick `OnStatus` can be published out of order; the next
  tick corrects it. Stamp statuses with their probe time and drop older ones on publish.
- `upgrade kubernetes` refuses a node Kubit cordoned during an interrupted Talos upgrade
  ("uncordon before upgrading"); only `upgrade talos` resumes and releases it. Release
  Kubit's cordon in `ApplyConfigs` too, or say in the message to rerun the Talos upgrade.
- The etcd check before a control plane goes down reads Talos's etcd service health,
  which refreshes every 20 s, so a member that failed moments earlier still counts as
  healthy. Add a direct quorum read per member if that window matters.
- A Talos upgrade that fails mid-run leaves the upgraded nodes with stored configs for
  the new version while the declaration keeps the old one, so they show "Config behind"
  and Apply would push the old version's configs back. Point the notice at resuming the
  upgrade, or save the target version before the first node.
- Traefik migration without an address pin (MetalLB off or the old IP outside the
  range): the `nginx` IngressClass is created once Traefik is up, unordered against the
  ingress-nginx uninstall, which deletes its own `nginx` class. If the uninstall is
  slower, the class is gone while tofu state keeps it. Re-plan after the migration, or
  recreate the class in a second apply step.
- Apply node configs now fails at the manifests step when the Kubernetes API does not
  answer, although every node config was applied; the CLI then skips the platform apply.
  Report it as a warning with a retry hint instead of failing the operation.
- `checkNodeAdd` (API) and `AddNode` place the new node in the declaration with
  different matching rules (hostname and IP vs hostname or IP). Share one helper.
- The status-bar uptime keeps ticking from the last `hello` while the daemon is
  stopped or unreachable; show it only while connected.
- Credentials default to the current directory: `cluster kubeconfig|talosconfig` (`./kubeconfig`),
  `cluster export` (plaintext `secrets.yaml`), `config render` (`./out`), `sops export`
  (`keys.txt`), `backup`. With the repo direction they belong in the cluster repo's ignored
  directory, or stdout.
- Security: JSON handlers accept any Content-Type, and bodiless POSTs (`daemon/stop`,
  `events/{id}/ack`) have no CSRF guard while loopback needs no token; check
  `Origin`/`Sec-Fetch-Site` on non-GET `/api/` requests in `ServeHTTP`.
- WebSocket `OriginPatterns` is same-origin plus the dev server; a reverse proxy that
  rewrites `Host` fails the check.
- `platform.argocd` is still parsed as `LegacyArgoCD` and folded into Flux on load;
  dropping it needs a migration of stored specs (Phase 6 moves specs to the repo anyway).
- Node remove leaves the removed node's subnet in the other nodes' firewall rules until
  the next apply; `kubit apply` after a removal should narrow them.
- `OnStatus` side effects (snapshot scheduling, cert checks) also run on pushed
  statuses. Harmless (gated), but a publish-only hook would be clearer.
- Phase 1 cut leftovers (2026-10-05):
  - Schema still carries `users`, `sessions`, `api_tokens`, machine `oob`/`provision`/
    `labhost`/`host`/`wol` columns, `snapshots.offsite`; drop them in Phase 6.
  - `kubit pxe` without `--repo` still asks the daemon; drop that path once every
    cluster lives in a repo.
- Converge follow-ups (2026-10-05):
  - Not yet run against a cluster: create, add, config, upgrades, removal, platform
    through `kubit apply`; the Lease lock; encrypted state on a real platform module.
  - Apply operations run in the CLI process and print to the terminal; record them in
    the operations table so the console's Activity shows CI and laptop applies.
  - The tofu provider lock file lives in `~/.kubit`; a fresh machine resolves providers
    again within the pinned ranges. Writing `.terraform.lock.hcl` into the repo would pin them.
  - A node declared with `ip:` = its DHCP lease is found by MAC when the lease moves only
    before it joins; afterwards the plan asks to update cluster.yaml or pin a static address.
  - Secrets editor: keys under sequences are listed but not editable; non-Secret files
    split keys on dots.
