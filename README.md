# Kubit

Declarative lifecycle manager for Talos Linux / Kubernetes clusters on bare-metal mini
PCs and VMs. Single Go binary: CLI, daemon, embedded web UI.

## Architecture

| Layer | Owner | Mechanism |
|---|---|---|
| Discovery, machine config, apply, bootstrap, kubeconfig, OS/K8s upgrades, drain, reset, etcd | Kubit | Talos API via `siderolabs/talos/pkg/machinery` + `client-go` |
| Platform add-ons: MetalLB, ingress-nginx, gVisor RuntimeClasses, metrics-server, cert-manager, ArgoCD | OpenTofu (`infra/platform/`), executed by Kubit with a pinned binary | `hashicorp/helm` for charts, `alekc/kubectl` for CRs |
| User workloads | ArgoCD (optional) | Generated `gitops/` app-of-apps |
| Escape hatch | Operator | `infra/talos/` export (talos provider HCL + `import {}`) and native artifacts (`secrets.yaml`, `talosconfig`, machine configs, kubeconfig) — never executed by Kubit |

`cluster.yaml` under `~/.kubit/clusters/<name>/` is the single declarative input.
Secrets are stored in SQLite, AES-GCM encrypted with a master key held in the macOS
Keychain.

Full design and phase plan: see the plan file this repo was built from
(`/Users/mikael/.claude/plans/specification-universal-kubernetes-modular-cloud.md`);
the sections below track what is actually implemented.

## Layout

```
cmd/kubit/        CLI entrypoint (cobra)
internal/api      HTTP JSON API + SPA hosting
internal/...      talos, config, factory, cluster, k8s, tofu, export, store, pxe (per phase)
web/              Vite + Preact + TypeScript + Tailwind; dist/ embedded via go:embed
hack/vm/          vfkit harness: Talos arm64 VMs on Apple Virtualization.framework
```

## Build

```
make build     # npm run build (if web/node_modules exists) + go build → bin/kubit
make test
```

Requires Go 1.26+, Node 20+ for the UI. Talos machinery pinned to v1.14.0
(Kubernetes 1.37.0 default).

## Dev VMs

`brew install vfkit` and `brew tap nirs/vmnet-helper && brew trust nirs/vmnet-helper &&
brew install nirs/vmnet-helper/vmnet-helper` (macOS 26: no root needed), then:

```
hack/vm/vm.sh create 1          # 2 vCPU / 4 GiB / 20 GiB disk, boots Talos ISO (schematic with siderolabs/gvisor)
hack/vm/vm.sh list              # shows IP from vmnet's DHCP lease (192.168.64.0/24)
hack/vm/vm.sh start 1 --no-iso  # after Talos has installed to disk
hack/vm/vm.sh destroy all
```

Networking: vfkit's built-in NAT (Virtualization.framework's NAT attachment) isolates
VMs from each other — a peer gets "no route to host" — so etcd never forms a quorum
and a Layer-2 VIP is unreachable. The harness therefore starts each VM under
`vmnet-run` (vmnet-helper), which drives vmnet.framework with isolation off: every VM
given the same `--start/--end-address` lands on one shared 192.168.105.0/24 segment
with the host. `NET=nat` forces plain NAT for single-node work. `socket_vmnet` is not
an option: it exposes a stream socket for QEMU, vfkit needs a datagram socket.

Verified: EFI boot of the Talos v1.14.0 `metal-arm64.iso` under Virtualization.framework
reaches maintenance mode and, after install, reboots from disk even with the ISO still
attached; the insecure `Version` call on :50000 answers
`tag=v1.14.0 arch=arm64 platform=metal`. The API is flaky for roughly the first two
minutes after boot (vmnet NAT settling; NTS lookups time out in the same window) and
stable thereafter — discovery must retry. No serial console output: the arm64 ISO
uses `ttyAMA0`, not the virtio console.

Image Factory schematic for `siderolabs/gvisor` (arm64 and amd64):
`d9ff89777e246792e7642abd3220a616afb4e49822382e4213a2e528ab826fe5`.

## cluster.yaml

```yaml
apiVersion: kubit.dev/v1
kind: Cluster
metadata: { name: dev }
spec:
  talosVersion: v1.14.0            # default: machinery's version
  kubernetesVersion: v1.37.0       # default: machinery's DefaultKubernetesVersion
  extensions: [siderolabs/gvisor]  # default: derived from platform.gvisor
  schematicID: ""                  # Image Factory schematic; created from extensions when empty
  controlPlane:
    vip: 192.168.64.9              # optional Layer-2 VIP shared by control planes
    endpoint: https://192.168.64.9:6443   # default: VIP, else first control plane IP
    allowScheduling: true          # default: true when fewer than 6 nodes
  network:
    podCIDR: 10.244.0.0/16
    serviceCIDR: 10.96.0.0/12
    nameservers: [192.168.64.1]    # optional; ResolverConfig on every node
    ntp: [time.cloudflare.com]     # optional; TimeSyncConfig on every node
  pools:                           # default when absent: controlplane + worker, no overrides
    - { name: controlplane, role: controlplane }
    - { name: worker, role: worker }
    - name: gpu                    # a pool owns role, labels, taints, extensions, disk policy
      role: worker
      labels: { workload: gpu }
      taints: { nvidia.com/gpu: "true:NoSchedule" }
      extensions: [siderolabs/nvidia-open-gpu-kernel-modules-lts]   # → its own schematic/installer
      installDisk: { selector: { minSize: 100GB, type: nvme } }
  nodes:
    - hostname: cp-01
      ip: 192.168.64.2             # where the Talos API answers; follows DHCP unless network: is set
      mac: "52:54:00:4b:49:01"     # machine identity (uplink MAC); uuid: is recorded too
      pool: controlplane           # replaces role: (still accepted → mapped to the default pool)
      arch: arm64                  # amd64 | arm64
      kvm: true                    # /dev/kvm present → also labelled for runsc-kvm
      installDisk: { path: /dev/vda }   # or a selector; empty = pool policy
      labels: { rack: a1 }         # merged over the pool's; taints:/annotations: likewise
    - hostname: gpu-01
      ip: 192.168.64.150
      mac: "52:54:00:4b:49:07"
      pool: gpu
      network:                     # static addressing (absent = DHCPv4Config on the uplink)
        addresses: [192.168.64.150/24]
        gateway: 192.168.64.1
        nameservers: [192.168.64.1]
        vlan: 0                    # > 0 → VLANConfig on the uplink, addresses move to the VLAN link
  backup:
    etcd: { interval: 6h, keep: 28 }   # scheduled etcd snapshots; interval 0 disables
  maintenance:                          # optional; gates disruptive operations
    window: "Sat,Sun 02:00-06:00"       # "<days> HH:MM-HH:MM", days Mon..Sun or daily, may cross midnight
    timezone: Europe/Stockholm          # IANA; empty = daemon host zone
  platform:
    metallb: { enabled: true, range: 192.168.64.200-192.168.64.220 }
    ingressNginx: { enabled: true }
    gvisor: { enabled: true }
    metricsServer: { enabled: true }
    certManager: { enabled: false }
    argocd: { enabled: false }
```

Generated machine configs are Talos 1.14 multi-document: the v1alpha1 core plus
`UnattendedInstallConfig` (per-pool installer image + CEL disk selector), `SysctlConfig`
(`user.max_user_namespaces=11255` for gVisor), `HostnameConfig`, `KubeNodeConfig`
(labels `kubit.dev/pool`, `sandbox.runtime/gvisor[-kvm]`, pool ∪ node labels, taints and
annotations; NoSchedule taint when control planes are dedicated), `LinkAliasConfig`
`uplink` (by MAC, else the first physical link), `DHCPv4Config` or `LinkConfig` +
default `RouteConfig` (+ `VLANConfig`) on the alias, `ResolverConfig`, `TimeSyncConfig`
and, on control planes with a VIP, `Layer2VIPConfig`. `config.Lint` reports advisory
findings (even/single control planes, small disks, mixed arch, ranges off-subnet or
overlapping, VIP or MetalLB range taken by another stored cluster).

## Secrets

`~/.kubit/kubit.db` (SQLite, WAL). Secret columns (secrets bundle, talosconfig,
kubeconfig, per-node machine config) are AES-256-GCM sealed with a 32-byte master key
kept in the macOS Keychain as service `kubit` / account `master-key`.
`KUBIT_MASTER_KEY` (base64) overrides everything; a `master.key` file (0600) in
`$KUBIT_HOME` is used when present or when no keyring is reachable (headless Linux,
systemd units) and is minted there automatically. `kubit serve` logs which source it
used; back that up (`kubit key export`).

## Discovery

`kubit discover 192.168.64.0/24` TCP-probes :50000, then tries the insecure maintenance
API. Nodes that answer are `maintenance` (inventory recorded: MAC of the link holding the
IP, arch, CPUs, RAM, disks, `/dev/kvm` presence, SMBIOS UUID and serial); nodes that
reject the insecure TLS handshake are `configured`. Results are upserted into the
`machines` table **keyed by uplink MAC**: a machine that reappears on a new DHCP address
keeps its row (`ips_seen` history, hardware, cluster membership) and, for cluster
members, raises a `machine.ip-changed` event; the Nodes page then offers *Update
address*. A stored cluster's VIP answering on :50000 is skipped. Rows without a MAC
(foreign `configured` nodes) are keyed `ip:<addr>`.

Verified in maintenance mode on Talos 1.14 (arm64 VM): `Version`, `Memory`, `CPUInfo`,
`Disks`, `LS`, and the COSI resources `block.Disk`, `hardware.Processor`,
`hardware.SystemInformation`, `network.LinkStatus`, `network.AddressStatus`,
`runtime.MachineStatus` all answer.

## Cluster lifecycle

`kubit cluster create -f cluster.yaml` runs: preflight (every node in maintenance mode,
arch matches) → schematic → secrets + per-node configs stored → apply to all nodes in
parallel → wait for each node to reboot into the installed system (boot time changes;
apid accepts cluster credentials *before* the install reboot, so neither a `Version`
answer nor stage `booting` is proof) → bootstrap etcd on the first control plane → etcd
healthy with all members → kubeconfig → all nodes Ready → platform apply. Re-running
`create` on a `provisioning`/`failed` cluster with the same nodes resumes: installed
nodes are skipped, an already-healthy etcd is not re-bootstrapped.

Kubelet registration takes 2–3 minutes after the API server starts (bootstrap-token
`Unauthorized` until the controller manager settles); this is normal Talos behaviour.

A node with static `network:` is applied on its maintenance-mode lease and awaited on
the static address; cluster.yaml and the machine row then switch to it. Node
operations: **rename** (drain → `HostnameConfig` without reboot → kubelet re-registers
→ old Node deleted → uncordon), **move to pool** (same role; label/taint re-apply, or a
single-node upgrade to the pool's installer when the extension set differs),
**re-address** (DHCP↔static; applies via whichever address answers, waits on the new
one, then restarts the kubelet on workers or reboots control planes — etcd and the
static pods keep the old address until restart; refused for the no-VIP endpoint node). `PUT
/clusters/{name}/pools` edits pools; `POST /config/design` proposes a declaration for a
set of machines (control planes = bare metal before VMs — a hypervisor is one failure
domain, detected from SMBIOS and shown as a VM/metal pill —, then the most alike,
smallest, non-KVM machines; lint `control-planes-on-vms` when two or more control
planes are virtual; VIP
`.250` and MetalLB `.200-.220` in the nodes' /24) and `POST /config/lint` returns
warnings for any declaration.

Other commands: `cluster apply` (regenerate + re-apply every machine config from
cluster.yaml, then platform), `node add`, `node remove` (drain → delete → graceful
reset; refuses to drop to 0 or, without `--force`, 2 control planes), `upgrade talos`,
`upgrade kubernetes` (config re-apply with new component images, control planes first),
`status`, `cluster export`.

Talos < 1.14 is rejected: Kubit only emits the multi-document config set.

## Platform layer (OpenTofu)

`~/.kubit/clusters/<name>/infra/platform/` is rendered from embedded templates plus a
`terraform.tfvars.json` from cluster.yaml, then `tofu init/plan/apply` with a pinned
binary (`internal/tofu.Version`, checksum-verified download into `~/.kubit/bin`). Charts
are pinned in `variables.tf`. Providers: `hashicorp/helm` 3.x, `hashicorp/kubernetes`
3.x (`*_v1` data sources), `alekc/kubectl` for CRs.

Findings baked into the templates:

- Talos enforces Pod Security `baseline` cluster-wide; `metallb-system` is created with
  `pod-security.kubernetes.io/enforce=privileged` or the speaker never starts.
- Talos labels control planes `node.kubernetes.io/exclude-from-external-load-balancers`,
  which MetalLB honours: on a cluster whose control planes carry workloads no
  LoadBalancer IP would ever be announced. The generator drops that label when
  `controlPlane.allowScheduling` is true.
- metrics-server runs with `--kubelet-insecure-tls` (Talos kubelets serve self-signed
  certs unless a serving-cert approver is installed).

Per-add-on Helm values: `platform.<addon>.values` in cluster.yaml is passed as `values = [yamlencode(...)]` only when non-empty, so declaring nothing never triggers a Helm upgrade.

Verified on a single-node VM: ingress-nginx reachable from the Mac on its MetalLB IP, a
`runtimeClassName: gvisor` pod boots gVisor, `kubectl top nodes` works, and a second
`kubit platform plan` reports no changes.

## Export

`kubit cluster export <name> -o dir` writes native artefacts and `infra/talos/` for the
`siderolabs/talos` provider (>= 0.11): secrets imported from `secrets.yaml` (with
`ignore_changes = [talos_version]`, otherwise the provider would regenerate the PKI),
machine configs fed verbatim via `machine_configuration_input`, kubeconfig read.
`talos_machine_bootstrap` is gated behind `bootstrap = false` because the provider fails
with `AlreadyExists` on a bootstrapped node. Verified: `tofu apply` on a live cluster is
a no-op and the following `tofu plan` reports no changes.

## Web UI and API

`kubit serve` (default `127.0.0.1:8080`; binding elsewhere requires a bearer token,
generated at start or `KUBIT_TOKEN`) hosts the SPA and `/api/v1`:

- `GET clusters`, `GET clusters/{n}`, `GET clusters/{n}/status|yaml|kubeconfig`
- `POST clusters` `{yaml, skipPlatform}` → operation; `POST clusters/{n}/apply|platform/plan|platform/apply|upgrade/talos|upgrade/kubernetes|export|nodes`, `DELETE clusters/{n}[/nodes/{host}]`
- `GET nodes`, `POST discover {targets}`, `GET nodes/{ip}/services|logs?service=&follow=`, `POST nodes/{ip}/reboot`
- `POST config/validate` (raw YAML → defaulted YAML), `POST config/draft {name, ips}` (topology recommendation → cluster.yaml)
- `GET operations[/{id}]`, `GET events` (SSE: every operation event and status change)

Long-running calls return `{operationId}` immediately; the operation's events are
persisted in the `operations` table and streamed. Operations are serialised per cluster.
A daemon restart marks operations left `running` as failed.

## PXE

`sudo kubit pxe --iface en0 [--talos-version v1.14.0] [--schematic ID]` answers PXE
firmware as a proxyDHCP (no addresses handed out; the LAN's DHCP stays authoritative),
serves iPXE binaries (`undionly.kpxe`, `ipxe.efi`, `ipxe-arm64.efi`, fetched once from
boot.ipxe.org into `~/.kubit/cache`) over TFTP, and on `:8069` an iPXE script that
boots the Talos kernel/initramfs of the profile with `talos.platform=metal`. Boot
assets are proxied from the Image Factory through the same cache, so a rack of machines
downloads them once. iPXE's own DHCP round is recognised (user class / option 175) and
pointed at the script instead of the binary. Needs root for UDP 67/69/4011 and a host on
the machines' L2 segment.

## Console (web UI)

Object tree in the sidebar: each cluster expands into Overview · Nodes · Workloads ·
Network · Storage · Add-ons · Operations · Settings; Fleet holds Inventory and PXE;
Activity lists every operation. A bottom **Activity drawer** (`a`) shows running
operations full-width: stepper on the left, searchable log on the right, Cancel/Retry.

Operations are structured: each declares its steps up front (`Sink.plan`), brackets
them with `begin/end/fail/skip`, and the API persists steps, log, request and an
artefact per operation (`operations.steps/artifact/request`). Undeclared steps are
derived from log attribution so older paths still get a stepper.

**Plan → review → apply.** Add-ons → *Plan changes* runs `tofu plan` and stores
`tofu show -json` parsed into a per-add-on diff (`internal/tofu/plan.go`). The review
page (`/clusters/<name>/addons/<planId>`) shows create/update/delete per resource with
attribute diffs (sensitive and known-after-apply marked, provider deprecation warnings
folded away) and *Apply these N changes* executes exactly that saved `plan.tfplan`
(`Manager.ApplyPlan`). A plan is refused when cluster.yaml changed after it or a newer
plan exists. Settings → *Save* only stores cluster.yaml; *Apply node configs* pushes
machine configs; platform changes always go through a reviewed plan. The CLI's
`platform apply` and `cluster create` still converge without review.

## Health watcher

`kubit serve` runs one watcher loop per ready cluster calling `Manager.Status` every
15 s. Each tick writes capacity samples (cluster totals and per node) and diffs the
previous status into events with a severity: `talos.unreachable`/`talos.back`,
`node.notready`/`node.ready`, `node.cordoned`, `api.unreachable`/`api.back`,
`etcd.unhealthy`/`etcd.members`/`etcd.leader`, `talos.version`/`kubelet.version`,
`lb.assigned`/`lb.lost`, `node.removed`. A recovery event acknowledges the alert it
clears. The first observation after a daemon start reports only what is currently
wrong, so restarts do not replay history, and an alert that is already open for the
same object and kind is never raised twice. `GET /clusters/{name}/status` serves the
watcher's latest result; `?fresh=true` forces a live query; it carries `observedAt`,
`lastSnapshotAt` and `snapshotInterval` so the Overview's *Observer* card shows how
far behind the watcher is.

### Service health (what runs in the cluster)

Every `--service-interval` (default 4× the watch interval, 60 s) the watcher lists
workloads, pods, claims, services and ingresses (`Manager.ServiceHealth`) and applies
the rules in `internal/watch/services.go`. Nothing is installed in the cluster; the
API server already knows all of this. Alerts carry the object as
`kind/namespace/name` and auto-resolve when the object recovers or is deleted:

| alert | when | clears with |
|---|---|---|
| `workload.unavailable` | Deployment/DaemonSet/StatefulSet ready < desired, ≥ 5 min old, on two consecutive collections | `workload.available` |
| `pod.crashloop` | container waiting in `CrashLoopBackOff`, or ≥ 3 restarts within 10 min | `pod.recovered` |
| `pvc.pending` | claim Pending for ≥ 5 min | `pvc.bound` |
| `service.no-endpoints` | selector service with no ready endpoint for ≥ 5 min | `service.endpoints` |
| `ingress.no-address` | MetalLB on, Ingress without an address for ≥ 5 min | `ingress.address` |
| `lb.pool-exhausted` (critical) | every MetalLB pool address allocated | `lb.pool-free` |

Kubit settings → *Ignore namespaces* silences these for e.g. `dev`/`ci`. The
Overview alert rows link to the object; Workloads, Network and Storage rows carry a
pill while an alert is open. `GET /clusters/{name}/service-health` returns the latest
collection and the open alerts.

## etcd snapshots and disaster recovery

`kubit etcd snapshot <cluster>` (UI: Backups → Take snapshot) streams `EtcdSnapshot`
from the first control plane with healthy etcd, verifies it (bbolt open, key count,
sha256), gzips (≈20×) and seals it with the master key under
`~/.kubit/clusters/<name>/snapshots/`, recorded in the `snapshots` table. The daemon
takes one every `backup.etcd.interval` while the cluster is ready and idle, pruning
scheduled snapshots to `keep` (manual and pre-upgrade snapshots are never pruned); a
schedule that slips past twice its interval raises `backup.stale`. `kubit etcd
list|download|restore`, `GET/POST /clusters/{name}/snapshots`, download of the plain
`.db` for `talosctl bootstrap --recover-from`.

**Restore** (`etcd.restore`, typed confirmation, `--yes` on the CLI) is the
disaster-recovery path: EPHEMERAL is wiped on every control plane (STATE keeps the
machine config, so they come back as members rather than in maintenance mode), the
snapshot is uploaded to the first control plane (`EtcdRecover`), etcd is bootstrapped
with `RecoverEtcd`, the other members rejoin, and Kubit waits for every node to be
Ready. Verified on `lab` (3 control planes): a ConfigMap created after the snapshot was
gone, LB addresses and workloads intact, ~3 minutes end to end.

## Lifecycle safety

- **Pre-upgrade checks** — every Talos/Kubernetes upgrade starts with `precheck`
  (API reachable, etcd healthy, all nodes Ready/uncordoned/Talos-reachable, ≥ 1 GiB
  free on `/var` per node, Talos target published by the Image Factory, and for
  Kubernetes the `apiserver_requested_deprecated_apis` metric checked against the
  target release — usage of an API removed in the target blocks the upgrade) and a
  `pre-upgrade` etcd snapshot.
- **Credentials** — `GET /clusters/{name}/certificates` parses the stored talosconfig
  and kubeconfig client certificates and the four CAs; `cert.expiring` (warn ≤ 30
  days, critical ≤ 7) is raised hourly per credential. `POST …/certificates/rotate`
  mints a fresh one-year talosconfig (`GenerateClientConfiguration`, endpoints
  rewritten to all control planes) or kubeconfig and replaces the stored copy.
- **Maintenance window** — `spec.maintenance.window` gates the disruptive routes
  (apply, upgrades, node drain/reboot/upgrade/remove/rename/pool/readdress, restore):
  outside the window the API answers 409 unless `?ignoreWindow=true`; the console
  shows the window notice in every confirm dialog and then overrides deliberately.
- **Alert forwarding** — Kubit settings → Alerts: health events at or above a
  severity are POSTed as JSON (`text` + `event`) to a webhook (Slack/Discord/Teams
  compatible) and/or mailed over SMTP (password sealed at rest); *Send test alert*
  exercises the sinks.
- **Audit log** — `audit_log` (every administrative action) is listed under Activity
  and each cluster's Operations tab, with CSV export.

## Off-site copies and the heartbeat

Kubit settings → *Off-site copies* names a second home for the DR material: a
**directory** (any mounted SMB/NFS share, USB disk or synced folder) or an
**S3-compatible bucket** (AWS, MinIO, Backblaze B2, Wasabi, Hetzner; secret key sealed
at rest). Every etcd snapshot is copied there as it is taken (`offsite` step; a failed
copy never fails the snapshot but raises `offsite.failed`, cleared by the next
success), local pruning removes the remote copy too, and once a day a sealed Kubit
backup (`backups/<ts>.kubitbak`, kept to *Keep daily Kubit backups*) is uploaded as the
`kubit.backup` operation. *Test target* does a write/read/delete round trip; the
Backups tab shows which snapshots have a copy. Everything off-site stays sealed —
`kubit key export` must be kept somewhere else again.

*Heartbeat (hours)* sends a summary to the alert sinks regardless of severity: per
cluster its state, open alerts and last snapshot age; the off-site status; and any
Talos/Kubernetes update available (also shown as a notice on each Overview). A
heartbeat that stops arriving means the daemon is down — the dead-man's switch.

## Console conventions

- **First run** (`/start`, also the home page while no cluster exists): per-arch ISO
  downloads built from the factory's vanilla schematic and the current stable Talos,
  the three steps to a cluster, and the network-boot page (`/fleet/pxe`, reachable
  from Inventory rather than the sidebar until PXE is verified on hardware).
- **Alerts carry runbooks**: every warn/critical kind has a *What to do* panel
  (`web/src/runbooks.ts`) — cause in one line, numbered steps, each linked to the
  place in Kubit where the action lives (node Actions tab, Backups, Add-ons, settings).
- **Navigation**: one line per cluster in the sidebar; the cluster's tabs live in its
  header. `⌘K`/`Ctrl+K` jumps to any cluster page, node or Kubit page; `a` toggles
  the Activity drawer; `/` focuses a table filter; `?` lists shortcuts. Theme follows
  the OS with a toggle in the status bar (remembered, applied before first paint).
- **Overview shows only what matters**: unacknowledged alerts with runbooks, alert
  history limited to alerts and their recoveries, five recent operations linking to
  Activity (filtered to the cluster). Info-only transitions stay in the events API.
- Tables show placeholder rows while loading and an explicit empty message after.
- **Nothing polls; everything is live.** One WebSocket (`/api/v1/ws`) carries every
  change. The store notifies on each write (`store.OnChange`) and `internal/api/live.go`
  turns that into typed messages — `cluster`, `clusterRemoved`, `machine`,
  `machineRemoved`, `snapshot`, `snapshotRemoved`, `audit`, `settings`, `healthAck`,
  `healthResolved` — alongside `status` (watcher tick), `health`, `operation`/`event`
  and `refresh {cluster, scope}` from Kubernetes informers (pods, workloads, services,
  endpoint slices, ingresses, claims, volumes, classes, nodes; debounced 1 s; running
  from `bootstrapped` on), the daemon-side PXE watch and the hourly `versions` check.
  The console keeps normalized live state (`web/src/store.ts`) that every view derives
  from, and refetches only large derived views when their scope fires. Messages carry
  sequence numbers: a reconnect replays from `?since=` out of a 2000-message ring, or
  gets `resync` and reloads base state once. Writes by another process (the CLI while
  the daemon runs) are detected daemon-side via SQLite's `data_version` and trigger
  `resync`. While disconnected the console shows a banner and the status dot pulses;
  the only timer in the UI is the "n min ago" clock.

## End-to-end script

`hack/e2e.sh <subnet> [--with-restore] [--teardown --vm-ids "1 2 3 4"]` drives a
running daemon through discover → design → create (3 control planes) → add worker →
rename → snapshot + verify → [restore drill] → crashloop alert → remove worker, every
step as an API operation visible in Activity, asserting with `kubectl` after each.
`--teardown` forgets the cluster and, with `--vm-ids`, recreates the hack/vm VMs so
the run repeats cleanly. It is the acceptance script for the hardware run.

## Running as a service

A cluster never depends on Kubit: Talos and Kubernetes run on their own and `cluster
export` hands over everything. Keeping the daemon running only adds the observer
features — alerts, scheduled etcd snapshots, watcher history — and the UI shows when
they lapse (Observer card, `backup.stale`). `kubit service install [--addr]` writes a
user unit and starts it at login: `~/Library/LaunchAgents/dev.kubit.serve.plist`
(launchd, `KeepAlive`, log in `~/.kubit/log/serve.log`) on macOS,
`~/.config/systemd/user/kubit.service` on Linux (`--system` for
`/etc/systemd/system`, run as root; `loginctl enable-linger` keeps a user unit alive
while logged out). `service status` / `service uninstall` manage it; the unit sets
`KUBIT_SERVICE=1`, shown as "service" in the status bar. `kubit serve` stops cleanly
on SIGTERM: running operations are recorded as cancelled, the listener drains and
the WAL is checkpointed.

## Backup and restore

`kubit backup -o file.kubitbak` (or Settings → Download backup) writes a tar.gz of
`~/.kubit` minus `bin/`, `cache/` and `.terraform/`, sealed with the master key
(AES-256-GCM; magic `KUBITBAK1`). The database's WAL is checkpointed first. Cluster
secrets are therefore double-sealed; kubeconfig/talosconfig files and tofu state are
sealed once. `kubit restore file` unpacks into an empty `KUBIT_HOME` (`--force` to
overwrite) and needs the same master key: `kubit key export` prints it for
`KUBIT_MASTER_KEY` on another machine.

## Status

- [x] Phase 0 — scaffold, `kubit version`, `kubit serve` (SPA + `/api/v1/version`), VM harness
- [x] Phase 1 — store, keyring crypto, `cluster.yaml`, machine config generation (`kubit config validate|render`)
- [x] Phase 2 — Talos client, `kubit discover <cidr|ip>...` (subnet scan + hardware inventory)
- [x] Phase 3 — `kubit cluster create` (resumable), `kubit node add`
- [x] Phase 4 — platform add-ons via OpenTofu (`kubit platform plan|apply`)
- [x] Phase 5 — `node remove`, `upgrade talos|kubernetes`, `status` (Talos 1.14.0→1.15.0-alpha.0 and Kubernetes 1.36.0→1.37.0 verified on a VM)
- [x] Phase 6 — `kubit cluster export`
- [x] Phase 7 — web UI (`kubit serve`): dashboard, create wizard, add/remove/upgrade dialogs, node logs, operations
- [x] Phase 8 — `kubit pxe` (proxyDHCP + TFTP + HTTP; unit-tested, not yet booted a physical machine)

Verified on vmnet-helper VMs: 3-control-plane create with a VIP (etcd 3/3 in 20 s, API
via the VIP, platform applied), `node add` worker and control plane, `node remove`
worker and — with `--force` — a control plane (graceful etcd leave, membership 3→2, node
back in maintenance mode), quorum guard refusing 3→2 without `--force`.

Not yet exercised: `kubit pxe` against a physical machine; `runsc-kvm` (no nested
virtualisation in the VMs); ArgoCD and cert-manager add-ons.
- [x] M1 — structured operations, Activity drawer, plan review/apply, IA skeleton, component library
- [x] M2 — node page: Overview (Talos + etcd member + Kubernetes requests), Hardware, Kubernetes (conditions, pods with usage, labels), Services, Logs, Actions (cordon/uncordon/drain/reboot[-with-drain]/upgrade node) — verified drain→reboot→uncordon on ha-worker-01
- [x] M3 — `internal/watch`: per-cluster poll (15 s, `--watch-interval`), `samples` (24 h fine / 30 d hourly) and `events` tables, SSE `status`/`health` pushes (UI no longer polls while connected), alerts with ack and auto-resolve on recovery, Overview capacity sparklines (1h–7d), `/versions` feed (Image Factory releases ≥ 1.14, Kubernetes minors supported by the built machinery) in Settings — verified: VM stop raised `talos.unreachable` within 15 s without reload, `node.notready` after the kubelet grace period, both cleared by `talos.back`/`node.ready` on restart
- [x] M4 — Workloads (controllers + pods, namespace filter, pod dialog with container logs and events), Network (addressing, MetalLB pool map with per-IP holder, services with endpoint counts, ingress host→service table), Storage (classes/PVCs/PVs, warning when no StorageClass) — verified with a demo Deployment + LoadBalancer + Ingress reachable from the Mac
- [x] M5 — add-on cards join cluster.yaml, tofu state (Helm release/chart/app version, status) and namespace readiness into one state (disabled/pending/deploying/ready/degraded/failed/orphaned); Configure dialog edits enabled/MetalLB range/Helm `values` (server-validated YAML) into cluster.yaml; `platform.<addon>.values` flows to `helm_release.values` only when set; Settings has a Form tab (endpoint, VIP, CIDRs, extensions, scheduling) beside YAML — verified: enabled ArgoCD with `server.replicas: 1` via UI → plan → reviewed apply → ArgoCD answering on its MetalLB IP; cert-manager verified in M1
- [~] M6 — Inventory: Adopt… opens the target cluster's add-node dialog preselected; PXE page reads the separate `kubit pxe` process's `/status.json` (server state, per-MAC boot stages dhcp → ipxe → kernel, log) and shows the exact sudo command when it is not running; Kubit Settings (`settings` table: factory URL, poll interval, discovery subnets, default MetalLB range, PXE status URL — applied live); `kubit backup`/`restore`/`key export` and a Download backup button — verified: backup restored into a fresh KUBIT_HOME manages the live cluster. **PXE boot itself is unverified** (see NOTES/backlog.md): pending real hardware
- [x] M8 — Onboarding & identity: machines keyed by MAC (migration v7, `ips_seen`, UUID/serial, WoL flag), node pools (role/labels/taints/extensions/disk policy → per-pool schematic), per-node DHCP or static addressing (+VLAN), cluster nameservers/NTP, `config.Design`/`Lint`, 5-step create wizard (Machines → Design → Network → Platform → Review), rename / move-to-pool / re-address operations, Inventory by machine with Retire and Wake-on-LAN, `/machines/<mac>` pages. Verified on 4 VMs: wizard with a `sandbox` pool (label + taint, static `.150`), rename, DHCP→static re-address of a control plane (reboot path) and static→static of a worker (kubelet-restart path). **Not exercised:** the `machine.ip-changed` path with a real DHCP lease change (vmnet leases are sticky; unit-tested in `internal/watch`), pool moves with a different extension set (re-image path shares `UpgradeNode`)
- [x] M9 — Lifecycle safety: scheduled/verified/sealed etcd snapshots with restore drill (verified live), credential inventory + rotation (verified), upgrade pre-checks + pre-upgrade snapshot (verified: refuses cordoned node and unpublished Talos version; passes on healthy `lab`), maintenance windows (verified 409/override), alert forwarding via webhook (verified with a local receiver; SMTP verified in M10) and audit UI. Application-layer add-ons (storage, monitoring) are intentionally left to Argo CD.
- [x] M10 — Service health & always-on: workload alerts from the API server (`internal/watch/services.go`: crashloop, unavailable workload, pending PVC, service without endpoints, ingress without address, exhausted MetalLB pool; age-gated, auto-resolving, deduplicated across restarts, ignore-namespaces setting), object links and row pills in the console, `GET …/service-health`; `kubit service install|status|uninstall` (launchd / systemd --user / --system), `master.key` file fallback for hosts without a keyring, SIGTERM drain + WAL checkpoint, Observer card (watcher freshness + last snapshot) and service/foreground indicator; SMTP with STARTTLS / implicit TLS / none via an explicit client. Verified on `lab`: crashloop, unavailable deployment, pending PVC, empty service and pool-exhaustion alerts raised, forwarded to a local webhook and resolved on delete/free; `kubit service install` → launchd running as "service" → uninstall; SIGTERM shutdown; SMTP delivery to a local receiver (plain mode; STARTTLS/465 code paths untested against a real provider). Not exercised: the systemd unit on a real Linux host (rendering is unit-tested); the `master.key` fallback beyond its unit test
- [x] M11 — Off-site & dead-man's switch: `internal/offsite` (directory and S3 targets, atomic dir writes, probe, retention), snapshot copy step + `offsite.failed`/`offsite.ok`, daily sealed Kubit backup upload (`kubit.backup` operation), settings section with test/copy-now/status, Backups tab off-site column, heartbeat summary incl. updates available, Overview update notice; also: sidebar tree and cluster Operations tab removed (Activity has a cluster filter), Overview events limited to alerts + recoveries. Verified: dir target round trip, snapshot copied, three backups pruned to two, heartbeat delivered to the webhook. Not exercised: a real S3 endpoint (minio-go; probe/list/put paths are straightforward but untested against a live bucket)
- [x] M12 — Product polish: VM-aware design (bare metal first, `control-planes-on-vms`), machine-centric wizard table (model, VM/metal, disk transport, NICs), runbooks on every alert kind, getting-started page with ISO downloads, ⌘K palette, shortcut sheet, theme toggle, loading placeholders, stale alerts reconciled after a daemon restart, `hack/e2e.sh`. Sidebar tree and cluster Operations tab removed; Overview limited to alerts + recoveries
- [x] M13 — Live everywhere: store change notifier, WebSocket transport with replay/resync, typed live state in the console (clusters, machines, snapshots, audit, settings, acks/resolves), external-writer detection, connection banner. Verified: sidebar pill provisioning → ready without reload, ack in one client clears in another, CLI `discover` and API retire reflected live, daemon stop → banner → reconnect + resync. Also found by the e2e script and fixed: an etcd restore left workers' pods (kube-proxy, MetalLB) with dead watches — restore now recreates every pod on workers
- [ ] M7 — tests, CI, packaging, docs
