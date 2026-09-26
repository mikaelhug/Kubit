# Kubit

Declarative lifecycle manager for Talos Linux / Kubernetes clusters on bare-metal mini
PCs and VMs. Single Go binary: CLI, daemon, embedded web UI.

## Architecture

| Layer | Owner | Mechanism |
|---|---|---|
| Discovery, machine config, apply, bootstrap, kubeconfig, OS/K8s upgrades, drain, reset, etcd | Kubit | Talos API via `siderolabs/talos/pkg/machinery` + `client-go` |
| Platform add-ons: MetalLB, ingress-nginx, gVisor RuntimeClasses, metrics-server, cert-manager, Longhorn, Flux | OpenTofu (`infra/platform/`), executed by Kubit with a pinned binary | `hashicorp/helm` for charts, `alekc/kubectl` for CRs |
| User workloads | Flux add-on (headless), syncing an apps repository you own | Kubit creates GitRepository and Kustomization `flux-system/flux-system` from `platform.flux.repository`; the path holds one Flux Kustomization per app, so a broken app fails alone. Kubit is the only UI and alerts on failed syncs. Example: [github.com/mikaelhug/kubit-apps](https://github.com/mikaelhug/kubit-apps) |
| App secrets | SOPS-encrypted files in the apps repository, one age key per cluster | Flux's kustomize-controller decrypts them in the cluster; Kubit generates, seals, backs up and installs the key |
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
  extensions: []                   # default: derived from platform.gvisor and platform.longhorn
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
      dataDisks: [/dev/vdb, /dev/vdc]   # whole disks → xfs volumes at /var/mnt/data-1, data-2
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
    flux:
      enabled: false
      repository:                       # optional; without it Flux runs with nothing to sync
        url: https://github.com/you/apps.git   # public HTTPS
        branch: main                    # default main
        path: ./flux                    # default ./; one Flux Kustomization per app
        interval: 5m                    # how often Flux fetches; default 5m
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

**Disk roles.** A node has one install disk and any number of *data disks*
(`dataDisks`, up to 8, never the install disk). Each data disk becomes a
`UserVolumeConfig` `data-N` of type `disk` — whole disk, xfs, selector
`disk.dev_path == "<path>"` (Talos does not supply `system_disk` when it matches a
whole-disk volume, so that term fails the volume) — that Talos formats on first use and
mounts at `/var/mnt/data-N`; the node is labelled `kubit.dev/data-disks: N`. Adding a
data disk to an existing node is a plain **Apply** (no reboot). The wizard's Design
step lists every non-install disk per machine with a checkbox (*Use all* claims the
lot), the add-node dialog does the same, and the node page shows path → mount. Pods
reach the volumes through the Longhorn add-on, `hostPath`, or a StorageClass the
operator deploys (local-path-provisioner pointed at `/var/mnt/data-*`, or a CSI).

**Storage on the system disk.** Most machines have one SSD. With `storage.systemDisk:
true` (set for every drafted cluster), a node without data disks keeps
`storage.ephemeralSize` (default `40GiB`) of its install disk for Talos's `/var`
(EPHEMERAL: images, logs, etcd) and gives the rest to a `data-system` partition, xfs,
mounted at `/var/mnt/data-system` and handed to Longhorn: the EPHEMERAL `VolumeConfig`
gets an absolute `maxSize` with `grow: false`, and a `UserVolumeConfig` selects
`system_disk` with `minSize: 10GiB` and `grow: true`. Talos sizes partitions only when
it creates them and never shrinks EPHEMERAL, so this takes effect on fresh installs;
an existing node needs to be removed and added again, so `storage` cannot change once
the cluster is installed. The split happens only while Longhorn is enabled. Design warns
(`small-system-disk`) when a disk leaves under 10 GiB after `/var`. Lab VMs default to
60 GiB sparse disks for this.

**Default add-ons.** A drafted cluster (wizard, lab host) comes up ready to run apps
from Git: MetalLB, ingress-nginx, metrics-server (Kubit's usage views read it),
cert-manager, Flux, Longhorn (on data disks or the system disk, so its Talos
extensions are in the first install and no re-image is needed) and Builds. gVisor is
opt-in. The
wizard's Platform step and the lab host dialog take the apps repository (URL and path);
Flux syncs it on the first platform apply. In the wizard, claiming the first data disk
turns Longhorn on and releasing the last turns it off; the Platform step can still
change either.

## Secrets

`~/.kubit/kubit.db` (SQLite, WAL). Secret columns (secrets bundle, talosconfig,
kubeconfig, per-node machine config) are AES-256-GCM sealed with a 32-byte master key
kept in the macOS Keychain as service `kubit` / account `master-key`.
`KUBIT_MASTER_KEY` (base64) overrides everything; a `master.key` file (0600) in
`$KUBIT_HOME` is used when present or when no keyring is reachable (headless Linux,
systemd units) and is minted there automatically. `kubit serve` logs which source it
used; back that up (`kubit key export`).

### App secrets (SOPS + age)

Kubit holds the **keys**, not the secrets, and is never in the data path, so a cluster
keeps working while the laptop sleeps.

- **Values** live in the apps repository as `*.sops.yaml` files: SOPS encrypts each value
  with AES-256-GCM under a random data key, which is wrapped for every age recipient
  listed in `.sops.yaml`. The ciphertext is safe in a public repo; names and structure
  stay readable, and history is permanent (after a key leak, change the values).
- **One key per cluster.** Kubit generates an X25519 age identity on the cluster's first
  platform plan (or the first `GET …/sops`) and keeps it sealed in `sops_keys`. That
  table has no foreign key, so the key outlives deleting the cluster: a cluster
  rebuilt under the same name gets the same key back and everything in Git decrypts
  again. It is in every Kubit backup.
- **Delivery.** Every platform apply with Flux enabled server-side applies Secret
  `flux-system/sops-age` (`age.agekey`; kustomize-controller only reads keys ending in
  `.agekey`) before `tofu apply`, even when the plan is empty, so a deleted Secret comes
  back on the next apply. It never passes through tfvars or tfstate. With Flux disabled
  the next apply deletes it.
- **Decryption.** Kubit's root Kustomization carries
  `decryption: {provider: sops, secretRef: {name: sops-age}}`, so kustomize-controller
  decrypts every SOPS-encrypted Secret in the build, inside the cluster. A `*.sops.yaml`
  Secret is listed under `resources` like any other file; kustomize's `namespace:`
  applies to it (Flux does not check the SOPS MAC by default). Kustomizations you add
  in Git set the same `decryption` block themselves.
- **Sharing.** Each file is encrypted to a list of recipients: your personal key (to
  edit) plus every cluster that should read it. There is no global key; adding a cluster
  means adding its recipient to `.sops.yaml` and running `sops updatekeys`.
- **Rotation** (manual): add the new key's recipient next to the old one and
  `sops updatekeys` every file, push; *Import key* (or `kubit sops import`) and apply the
  platform; then drop the old recipient and `sops updatekeys` again.
- **Surfaces:** Add-ons → Flux shows the recipient with *Copy*. *Export key* and
  *Import key* are admin only and audited. `kubit sops recipient|export|import`.

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

**Machine kinds.** A row's *kind* is derived, never stored (`store.Machine.Kind()`),
and travels with every machine over REST and the socket (`kind`, `talos`). The rules
are ordered: `cluster != ""` → **member**; `labhost != nil` (any lab-host state) →
**labhost**; state `maintenance` → **maintenance**; state `configured` → **configured**
(Talos with a config Kubit did not apply; no credentials to query it); armed with Boot
into Talos, or state `booting`/`installing` → **booting**; everything else (`amt`,
`off`, `unknown`) → **unbooted**. `talos` is true only for member and maintenance — the
kinds whose Talos API Kubit can talk to. A lab VM (`host` set) is a modifier, not a
kind: it is unbooted while off, booting, then maintenance or member like any machine.
The node endpoints (`/nodes/{ip}/inventory|services|logs|reboot`) answer 409 with one
line for the other kinds instead of dialing :50000, and 502 when the port is closed;
gRPC failures are shortened to their message and code. The watcher probes only
maintenance/configured rows (Talos) and unbooted rows (AMT port); the PXE decision boots
a lab host from its own disk in every lab-host state, so a stale arm flag can never
re-image a host mid-setup.

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

`upgrade talos` also re-images. The schematic is recomputed from the current
extensions (Image Factory IDs are content hashes); when it differs from what the nodes
were installed with, every node gets its regenerated machine config and then the new
installer, even at the same Talos version. `GET /api/v1/clusters/{name}/image` reports
installed vs desired, and Lifecycle says when the nodes are behind.

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
- **MetalLB is layer-2 only.** Chart 0.16 enables FRR-K8s (a BGP daemonset, five
  containers per node) by default; Kubit only configures an `L2Advertisement`, so
  `internal/tofu/render.go` merges `frrk8s.enabled=false` and `speaker.frr.enabled=false`
  under the user's `platform.metallb.values`. A 3-node cluster runs 16 pods, not 20.
- **Every add-on carries resource requests** (MetalLB 20m/64Mi, ingress-nginx
  50m/128Mi single replica, metrics-server 20m/48Mi) so the pods are Burstable: on a
  starved node Talos's OOM controller evicts BestEffort work first instead of killing
  the platform in a loop, and a node that cannot fit them leaves them Pending, which
  the service health reports. User `values` override any default key by key.
- `helm_release` is `atomic`: a failed install uninstalls itself, so a transient API
  blip never leaves a release that blocks the next apply with "cannot re-use a name".
- **Longhorn** (`platform.longhorn`, chart 1.10.1) is the storage add-on: replicated
  block volumes, the default StorageClass, snapshots, backups to S3. It runs on the
  nodes' **data disks**, or on the system disk's `data-system` partition — Kubit labels
  every node at machine-config time (`node.longhorn.io/create-default-disk: config`
  plus a `node.longhorn.io/default-disks-config` annotation listing `/var/mnt/data-N`
  or `/var/mnt/data-system`, or `false` on nodes without storage) and the chart runs
  with `createDefaultDiskLabeledNodes`, so replicas never land in `/var`. Every
  Longhorn disk is a volume of its own, so the reserve Longhorn keeps for the OS on a
  default disk is 5 % instead of 30 % (`storageReservedPercentageForDefaultDisk`); on a
  first lab run the 30 % left 12 of 17 GiB schedulable per node and the 20 GiB
  registry volume never scheduled. Enabling
  it adds the `siderolabs/iscsi-tools` and `siderolabs/util-linux-tools` extensions
  to the cluster schematic (picked up by new nodes and the next Talos upgrade), and
  refuses a cluster where no node has storage. On an existing cluster the order is
  enforced: after enabling Longhorn, Lifecycle and Add-ons say the nodes' image lacks
  the extensions, and platform plan/apply refuse until **Upgrade Talos** (at the same
  version is fine) has re-imaged the nodes. That upgrade first applies each node's
  regenerated machine config, so the Longhorn disk labels land before the chart
  installs; Longhorn only reads them when it first registers a node. Default replica
  count is 3 or the
  number of storage nodes. `longhorn-system` is created `privileged` like
  `metallb-system`. In multi-document Talos configs the kubelet document forbids
  `.machine.kubelet.extraMounts`, which is why the classic `/var/lib/longhorn` bind
  mount is not used and user volumes carry the data instead. Verified on a lab cluster
  on this Mac (2026-09-26): two workers with 20 GiB data disks give Longhorn two
  schedulable 19 GiB disks, and an app volume keeps two healthy replicas.

- **Builds** (`platform.builds`) builds images inside the cluster, since Flux only
  deploys. Namespace `kubit-builds` (privileged) holds a `registry:3` on a Longhorn
  volume (5 GiB) and a rootful `moby/buildkit` daemon (`buildkitd:1234`, cache in a
  10 GiB emptyDir, memory limit 1.5 GiB). The registry's LoadBalancer takes the last
  address of the MetalLB range from a `builds` pool with `autoAssign: false`; the
  default pool stops one address earlier and the L2Advertisement announces every
  pool. Every node gets a Talos `RegistryMirrorConfig` `registry.kubit` →
  `http://<that address>:5000` with `skipFallback` (applied live; a platform apply
  updates nodes whose stored config lacks it), so pods pull
  `registry.kubit/<app>:<version>`. A build is a Job in the apps repository that runs
  `buildctl` against the daemon with a Git context (`<repo>#main:apps/<app>/src`) and
  pushes to `registry.kubit-builds.svc:5000/<app>:<version>`; its Flux Kustomization
  has `wait` and `force`, and the app's Kustomization `dependsOn` it, so the app rolls
  once the image exists and a new version re-creates the Job. The Builds card lists
  the Jobs with state and logs. Needs MetalLB and Longhorn. Trade-offs: builds run as
  root on a node, the daemon and the registry have no authentication (the registry is
  readable and writable from the whole LAN on its MetalLB address, and every node
  pulls `registry.kubit/*` from it), no registry garbage collection, public
  repositories only. Plan and apply refuse while a node config lacks the mirror (turning
  Builds on for an existing cluster: apply node configs first), and the MetalLB range
  cannot change while Builds is on.
- **Flux, headless** (`platform.flux`, `fluxcd-community/flux2` chart 2.19.1, Flux
  2.9.5). Source, kustomize, helm and notification controllers only: the image
  automation and reflector controllers are off, and there is no UI; Kubit's Flux card
  shows controller readiness, every GitRepository, OCIRepository, HelmRepository,
  Kustomization and HelmRelease (Ready, revision, message; pushed live from informers
  that start when Flux's CRDs appear) and the SOPS recipient. An object not Ready for
  two service collections raises `flux.not-ready` with Flux's first message line;
  three Ready collections resolve it. Suspended objects never alert. The CRDs carry `helm.sh/resource-policy: keep`, so uninstalling
  the chart never cascades into the objects. With `platform.flux.repository` set, Kubit
  applies GitRepository and Kustomization `flux-system/flux-system` (prune on,
  `deletionPolicy: Orphan`): removing a folder prunes it, but disabling Flux or
  clearing the repository leaves the running apps alone. The path should hold one Flux
  Kustomization per app (kubit-apps: `flux/<app>.yaml` → `apps/<app>/`, each with
  `decryption` and `prune`): a single Kustomization over every folder applies all or
  nothing, and in a five-user run on `lab` one malformed Deployment held back every
  other push until it was fixed. Changing the path re-owns everything: the root
  Kustomization prunes what it applied under the old path (namespaces and volumes
  included) before the new Kustomizations re-create it. A failed chart pull shows on the
  OCIRepository or HelmRepository while the HelmRelease stays Ready on the old chart,
  which is why sources are listed. The GitRepository is created only after every other
  platform release is up: on a fresh cluster the first sync once raced ingress-nginx's
  admission webhook and both HelmReleases failed their install. Kustomize's `helmCharts` is not available; Helm
  charts are HelmRelease objects. The controllers run with cluster-admin, so anything in the
  repository can change anything in the cluster.

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

`kubit serve` (default `127.0.0.1:8080`) hosts the SPA and `/api/v1`. Who may call it:

### Identity and roles

- **Accounts** live in Kubit (`users`, `sessions` tables; bcrypt passwords, sessions
  and API tokens stored as SHA-256). Three roles: **viewer** (reads everything except
  credentials: kubeconfig, talosconfig, export), **operator** (viewer + every
  operation: create, add, upgrade, power, lab hosts), **admin** (operator + accounts,
  Kubit settings, backup/restore/key). Enforced per request by method and path in
  `internal/api/auth.go` (`requiredRole`); a refusal is `403 {code: forbidden}`.
- **Fresh install:** while no account exists, a loopback caller is the implicit
  administrator (`via: loopback`), so the UI works before anyone signs up; Settings →
  Accounts says so and *Add account* creates the first administrator (`POST
  auth/setup`, once). From then on every request needs a session or token, including
  from localhost. A non-loopback bind without accounts still needs the start-up bearer
  token (`--token` / `KUBIT_TOKEN`), which stays an administrator credential for
  automation.
- **Sign-in:** `POST auth/login {name, password}` sets an HttpOnly session cookie
  (30 days); `POST auth/logout` revokes it; `GET auth/me` says who you are, whether
  setup is pending and which SSO is offered. Disabling an account ends its sessions
  and tokens at once; the last enabled administrator cannot be demoted, disabled or
  deleted.
- **API tokens:** Settings → Accounts → *Tokens* issues `kbt_…` bearer tokens with the
  account's role and an optional expiry, shown once. Give one to the PXE service
  (`KUBIT_TOKEN`) once accounts exist; `/api/v1/labhost/*` and `/api/v1/pxe/decide`
  stay open because the Debian installer and the PXE process call them without
  credentials (they carry the SSH public key and boot decisions, nothing secret).
- **Single sign-on:** Settings → *Single sign-on* takes an OpenID Connect issuer,
  client ID/secret (sealed), the username and groups claims, and which provider groups
  map to admin / operator / viewer (plus a default role for everyone else, or no
  access). `GET auth/oidc/start` runs the authorization-code flow with PKCE; the
  callback verifies the ID token, creates or updates the account (`source: oidc`, no
  password) and re-applies the role from the groups on every sign-in. Tested against
  an in-process provider (`internal/api/oidc_test.go`).
- **Audit:** every entry records the actor (`audit_log.actor`), shown as *Who* on the
  audit log; sign-ins, failures and refusals are entries too.
- **Cluster SSO for kubectl:** `spec.auth.oidc` (cluster Settings → form: SSO issuer,
  client ID, claims, admin group) renders an `AuthenticationConfiguration` document
  for the API server (`KubeAuthenticationConfig`, one JWT authenticator; users and
  groups prefixed `oidc:`) on control planes, and the platform layer binds
  `oidc:<adminGroup>` to `cluster-admin` (`oidc.tf`). Applies on *Apply node configs*
  and *Apply platform*. People then use kubectl with an OIDC kubeconfig (kubelogin)
  instead of the shared admin credential.

### Endpoints

- `GET clusters`, `GET clusters/{n}`, `GET clusters/{n}/status|yaml|kubeconfig`
- `POST clusters` `{yaml, skipPlatform}` → operation; `POST clusters/{n}/apply|platform/plan|platform/apply|upgrade/talos|upgrade/kubernetes|export|nodes`, `DELETE clusters/{n}[/nodes/{host}]`
- `GET nodes`, `POST discover {targets}`, `GET nodes/{ip}/services|logs?service=&follow=`, `POST nodes/{ip}/reboot`
- `POST config/validate` (raw YAML → defaulted YAML), `POST config/draft {name, ips}` (topology recommendation → cluster.yaml)
- `GET operations[/{id}]`, `GET events` (SSE: every operation event and status change)
- `GET clusters/{n}/flux` (GitRepositories, OCIRepositories, HelmRepositories, Kustomizations, HelmReleases with their Ready condition and revision; refreshed by the `flux` scope)
- `GET clusters/{n}/sops` (the age recipient), `GET|PUT clusters/{n}/sops/identity` (admin: export, or import `{keys}`)
- `GET auth/me`, `POST auth/setup|login|logout`, `GET auth/oidc/start|callback`; `GET/POST users`, `PUT/DELETE users/{name}`, `GET/POST users/{name}/tokens`, `DELETE users/{name}/tokens/{token}` (admin)

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
pointed at the script instead of the binary. BIOS firmware and iPXE get the option 43
discovery bypass; UEFI firmware gets a plain proxy offer and comes back to the boot
server on :4011 (the variant every firmware supports). Needs root for UDP 67/69/4011
and a host on the machines' L2 segment.

## Console (web UI)

One page per question. Sidebar: **Home** · Clusters (one line each) · Lab hosts (one
line each) · Fleet: Inventory, Network boot · Kubit: Activity, Settings.

| route | question it answers |
|---|---|
| `/` Home | Is everything I run all right, what needs me? Open alerts across every cluster and lab host, Kubit notices (no accounts, PXE down while a machine is armed, off-site failing, updates available), cluster and lab host cards, machines by next step, running and recent operations. With nothing known it shows the three steps to a cluster. |
| `/clusters/<name>/…` | One cluster: Overview (health, alerts with runbooks, cards, capacity), Nodes, Workloads · Network · Storage (read-only Kubernetes views), Add-ons (plan → review → apply), Backups (etcd snapshots, schedule, restore), **Lifecycle** (upgrades, credentials, export, forget), Settings (the declaration: form, YAML, pools, apply node configs). |
| `/machines/<mac>` | One machine, rendered by kind: Overview · Hardware · Kubernetes · Services · Logs · Actions, tabs only where they can answer. Actions are grouped *Node* (cordon … remove) and *Machine* (remote management, VM controls, Wake-on-LAN, adopt, make lab host, retire). `/nodes/<ip>` redirects here. |
| `/labhosts/<mac>/…` | One lab host: Overview (alerts, utilisation, System with Update/Reboot host), VMs (start, stop, resize, re-provision, delete, add), Actions (add VMs, remote management, release). |
| `/fleet/inventory` | The hardware ledger: every physical machine by MAC, grouped by what happens next (Available · Needs boot · In use), with lab VMs behind a toggle. Scan, add by remote management, ISO links, bulk *Boot into Talos*. |
| `/fleet/network-boot` | The PXE server: state and command, enrollment switch, machines that booted through it. |
| `/operations` | Activity: Operations · Audit, filtered per cluster; `/operations/<id>` shows steps and log. |
| `/settings/<page>` | This installation: General, Discovery, Alerts, Off-site, Accounts, Single sign-on, Backup. Each page saves only its own fields and keeps unsaved edits while you look at another page. |

**Namespaces.** Workloads, Network and Storage open on **Apps**: every namespace that is
not the platform. **Platform** is `kube-system`, `kube-public`, `kube-node-lease` and the
namespaces of Kubit's add-ons (`metallb-system`, `ingress-nginx`, `cert-manager`,
`flux-system`, `longhorn-system`; `cluster.PlatformNamespace`, served with each namespace's
Pod Security level by `GET /api/v1/clusters/{name}/namespaces`); the `kubernetes` API
Service in `default` counts as platform too. **All** shows both. A namespace picker narrows
to one namespace; scope and namespace live in the URL (`?scope=`, `?ns=`), so alert
links land filtered. Kubit creates namespaces only for its add-ons: an app's namespace
belongs in Git next to the app (a Namespace manifest in the app's folder).
Workloads also lists CronJobs; Jobs and CronJobs never count as unavailable controllers.

A bottom **Activity drawer** (`a`) shows running operations full-width: stepper on the
left, searchable log on the right, Cancel/Retry.

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
15 s. Each tick writes capacity samples (cluster totals and per node; usage is
metrics-server's whole-node reading, so its ceiling is node capacity, not allocatable) and diffs the
previous status into events with a severity: `talos.unreachable`/`talos.back`,
`node.notready`/`node.ready`, `node.cordoned`, `api.unreachable`/`api.back`,
`etcd.unhealthy`/`etcd.members`/`etcd.leader`, `talos.version`/`kubelet.version`,
`lb.assigned`/`lb.lost`, `node.removed`. A recovery event acknowledges the alert it
clears. The first observation after a daemon start reports only what is currently
wrong, so restarts do not replay history, and an alert that is already open for the
same object and kind is never raised twice. `node.memory-small` (warn) fires for a
registered node with under 768 MiB allocatable — a 1 GiB VM keeps ~450 MiB after Talos
and the kubelet, not enough for the platform add-ons — and clears with `node.memory-ok`
once it is resized. `GET /clusters/{name}/status` serves the
watcher's latest result; `?fresh=true` forces a live query; it carries `observedAt`,
`lastSnapshotAt` and `snapshotInterval` for the Overview's Backups card.

**Confirmation, gaps and the blind observer.** A reachability fact (`talos.unreachable`,
`api.unreachable`, `etcd.unhealthy`, `node.notready`) becomes an alert only after it has
held for three consecutive ticks with no gap between them (`internal/watch/confirm.go`,
45 s at the default interval); a recovery is recorded at once, and only if the alert was
raised. A tick that arrives more than twice the interval after the previous one is a
*gap*: the laptop running Kubit slept, which is a normal thing for it to do. Kubit is a
tool, not a service the clusters depend on: on wake the tick re-baselines, alerts start
counting again from zero, a due snapshot is taken then, and `backup.stale` is not
raised for time Kubit was asleep (gaps are counted, `GET /observer`).
Every dial failure is classified (`internal/cluster/reach.go`): a refusal or timeout is
the target's problem, `EHOSTUNREACH`/`ENETUNREACH`/`EHOSTDOWN` is the observer's. When
every probe in a status fails for the observer's reason *and* the default gateway
cannot be dialed either, the status is `observer: offline`: no cluster alert is raised,
`status.health` is `unknown`, and one `observer.offline` warn is filed under the `kubit`
pseudo-cluster (cleared by `observer.online`). Scheduled snapshots skip such ticks and a
failed attempt waits ten minutes before the next. `kubit serve` takes `serve.lock` in
`KUBIT_HOME`; a second daemon on the same home refuses to start (two watchers would
double every sample and alert). Found on the EliteDesk lab: the MacBook running the daemon
slept some forty times a day and each DarkWake tick had raised a critical alert that
cleared on the next wake; separately, one detached daemon process lost LAN access
altogether while every other process on the Mac could reach the cluster.

**Health verdict.** Every tick on a ready cluster also sets `status.health`:
`down` when a confirmed alert about the API, etcd or a node is open, `degraded`
when unacknowledged warn/critical alerts are open (`status.openAlerts`), `unknown`
while the observer is offline, else `healthy`. The cluster's lifecycle `state` stays
`ready`; the console's cluster pill shows the verdict over it. `status.lastContactAt`
is the last observation in which anything answered; the console shows it as "seen
12 s ago" in the cluster header and a *Check now* button runs `?fresh=true`.

### Service health (what runs in the cluster)

Every `--service-interval` (default 4× the watch interval, 60 s) the watcher lists
workloads, pods, claims, services and ingresses (`Manager.ServiceHealth`) and applies
the rules in `internal/watch/services.go`. Nothing is installed in the cluster; the
API server already knows all of this. For 10 minutes after a disruptive operation
(create, apply, upgrade, reboot, add-on apply) the collection still refreshes what the
console shows, but no workload alerts are derived. Alerts carry the object as
`kind/namespace/name` and auto-resolve when the object recovers or is deleted:

| alert | when | clears with |
|---|---|---|
| `workload.unavailable` | Deployment/DaemonSet/StatefulSet ready < desired, ≥ 5 min old, on two consecutive collections | `workload.available`, after three consecutive healthy collections |
| `pod.crashloop` | container waiting in `CrashLoopBackOff`, or ≥ 3 restarts within 10 min | `pod.recovered`, after 10 quiet minutes |
| `pvc.pending` | claim Pending for ≥ 5 min | `pvc.bound` |
| `service.no-endpoints` | selector service ≥ 5 min old with no ready endpoint on two consecutive collections | `service.endpoints`, after three consecutive healthy collections |
| `ingress.no-address` | MetalLB on, Ingress ≥ 5 min old without an address on two consecutive collections | `ingress.address`, after three consecutive healthy collections |
| `lb.pool-exhausted` (critical) | every MetalLB pool address allocated | `lb.pool-free` |

Raising takes two bad collections and clearing three good ones (`raiseAfter`,
`clearAfter`), so a pod restarting every minute is one open alert, not a warn/recovery
pair, toast and webhook per minute.

Kubit settings → *Ignore namespaces* silences these for e.g. `dev`/`ci`. The
Overview alert rows link to the object; Workloads, Network and Storage rows carry a
pill while an alert is open. `GET /clusters/{name}/service-health` returns the latest
collection and the open alerts. The Overview's **Workloads** card (pod count, unavailable
controllers, failing pods) opens Workloads on the Pods view (`?view=pods`), which has a
node filter (`&node=<hostname>`, also reached from the Nodes tab's pod count) and links
each pod's node to its node page. Workloads stays read-only: exec, edit and delete belong
to kubectl/k9s/Headlamp with the kubeconfig from Settings → Export.

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

- **Objects have one URL.** Cluster, machine (by MAC) and lab host pages are canonical;
  old paths (`/nodes/<ip>`, `/machines/<lab host mac>`, `/fleet/pxe`, `/start`,
  `/settings`) redirect. Actions live where their object lives and nowhere else: the
  create wizard only picks available machines; booting, adding VMs and lab-host
  maintenance happen in Inventory and on the lab host page.
- **Alerts carry runbooks**: every warn/critical kind has a *What to do* panel
  (`web/src/runbooks.ts`) — cause in one line, numbered steps, each linked to the
  place in Kubit where the action lives (node Actions tab, Backups, Add-ons, settings).
- **No replayed popups**: a fresh page load asks the daemon for the head of the message
  ring only, and anything replayed on a reconnect updates state without a toast; a
  health toast needs a live, unacknowledged event.
- **Machine pages render by kind**: a lab VM links to its host and carries the host's
  start/stop/re-provision controls; an unbooted or configured machine shows what it is
  waiting for; Services, Logs and Kubernetes tabs appear only where they can answer.
  Inventory pills read the kind (`lab host · ready`, `lab host · offline` once SSH has
  failed three checks while the stored state stays ready — the pill and the host's
  graphs turn red, the last readings and VM states go muted, and every action that
  needs SSH is disabled until it answers; `not running Talos · off`,
  `boot→Debian` while a Debian install is armed). Adopt, Retire, Make lab host and Boot
  into Talos are offered only where the daemon would accept them (`web/src/machine.tsx`,
  `groupOf` decides the Inventory group).
- **Navigation**: one line per cluster in the sidebar, and one per lab host (state
  pill, VM count on hover) as soon as one exists; the cluster's tabs live in its
  header. `⌘K`/`Ctrl+K` jumps to any cluster page, machine, lab host or settings page; `a` toggles
  the Activity drawer; `/` focuses a table filter; `?` lists shortcuts. Theme follows
  the OS with a toggle in the status bar (remembered, applied before first paint).
- **Overview shows only what matters**: unacknowledged alerts with runbooks, alert
  history limited to alerts and their recoveries, five recent operations linking to
  Activity (filtered to the cluster), a Backups card (last snapshot, schedule, off-site
  copy). Watcher freshness is "observed n s ago" in the cluster header. Info-only
  transitions stay in the events API.
- Every table is the shared `DataTable` (`web/src/components/DataTable.tsx`): one row height,
  cells vertically centred, two-line cells as a stacked name and muted detail. Tables show
  placeholder rows while loading and an explicit empty message after.
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

## Linux QEMU lab

`hack/qemu/lab.sh` puts four Talos amd64 VMs (2 vCPU, 2.5 GiB, QEMU/KVM, OVMF) on a
bridge `kubit0` at 192.168.105.1/24 with dnsmasq DHCP and NAT on any Linux box with
KVM (`net up`, `iso`, `create`, `start`, `wait`, `ip`, `stop`, `destroy`; `net down`
removes the bridge and the masquerade rule), for running `hack/e2e.sh
192.168.105.0/24 --with-restore` against a daemon on that host. **Unverified** —
written on macOS, where hack/vm (vfkit) is the harness. There is no CI: builds, tests
and releases are run locally (`make`).

## Remote management (Intel AMT, Redfish BMCs) and member-aware PXE

Machines with a management engine can be managed out-of-band from the machine page →
*Remote management* (or Inventory → *Add by remote management* before Talos ever
booted: the engine reports MAC, model and serial): **Power on / off / hard reset** and
**Boot into Talos** — the engine forces one network boot and Kubit's PXE server hands
that MAC Talos in maintenance mode (refused for cluster members, lab hosts and lab VMs;
the button says why). Credentials are sealed per machine (`machines.oob`, `type: amt |
redfish`). Two backends in `internal/oob`:

- **Intel AMT** (vPro desktops, NUCs) over WS-Management
  (`github.com/device-management-toolkit/go-wsman-messages`, digest auth, 16992/16993).
  AMT shares the host's wired NIC and address. Setup on the box: enable AMT in the
  BIOS, set the MEBx password (Ctrl+P), allow network access.
- **Redfish** (Dell iDRAC, HPE iLO, Lenovo XCC, Supermicro, OpenBMC — any DMTF
  conformant BMC) over plain HTTPS + basic auth on the BMC's own address, stdlib
  client, self-signed certificates accepted. `Probe` reads the service root, the first
  `ComputerSystem` (manufacturer, model, serial, UUID, power state, CPU count, memory),
  the first host NIC with a MAC, and every drive under `Storage` (model, size,
  protocol, media — no device path until Talos boots; the Hardware tab says so).
  `Power` posts `ComputerSystem.Reset` with `On / ForceOff / ForceRestart / PowerCycle`,
  falling back to what the BMC's `ResetType@Redfish.AllowableValues` lists
  (`GracefulRestart` when `ForceRestart` is absent); *Boot into Talos* PATCHes
  `Boot.BootSourceOverrideEnabled=Once, BootSourceOverrideTarget=Pxe` and refuses up
  front when the BMC does not list `Pxe`. Errors carry the BMC's
  `@Message.ExtendedInfo` text. Tested against an in-process fake BMC
  (`internal/oob/redfish_test.go`); **unverified on a real BMC**.

Discovery sweeps the addresses that did not answer as Talos: port 16992 → AMT, else an
unauthenticated `GET /redfish/v1` → BMC. With the default credentials from Kubit
settings (*Default AMT user/password*, *Default BMC user/password*, sealed) the engine
is asked who it manages and the machine row is created with its MAC; a BMC without
credentials is only logged, since unlike AMT it has no ARP shortcut to the host's MAC.
Include the BMC management subnet in the scan. The `machine.power` operation shows in
Activity; every request and reply is mirrored into its log as `amt:` / `redfish:` lines.
*Boot into Talos* arms one network boot the way Intel's own console does (verified on
an EliteDesk 800 G3, AMT 11): clear the boot source, write `AMT_BootSettingData` back
as the firmware reported it with IDE-R/SOL and the other one-shot options off (a
fixed property set is refused by AMT 11/12 as `InvalidRepresentation`; a refused write
is retried with the AMT 11 base set), give `Intel(r) AMT: Boot Configuration 0` the
`IsNextSingleUse` role, set `Force PXE Boot`, reset. The role call is the one that
makes the BIOS honour the source; AMT answers a wrong instance name with `4 Invalid
Reference` and boots normally, so a non-zero return fails the operation. Every request
and reply is mirrored into the operation log as `amt:` lines.

Install the PXE server once as a root service — `sudo kubit service install --pxe
--iface en0 --kubit-url http://127.0.0.1:8080` (launchd system daemon / systemd unit;
`service uninstall --pxe` removes it) — the only sudo Kubit ever needs; the Network
boot page prints the exact command while it is not running. The machine finds it by
broadcast: UEFI network boot sends a DHCP request, the LAN's DHCP answers with the
address and Kubit's proxyDHCP adds the boot file, so the server must sit on the
machines' VLAN and nothing is configured on the machine or the router.

The PXE server asks the daemon per MAC (`GET /api/v1/pxe/decide`, `--kubit-url`,
`KUBIT_TOKEN`): **cluster members get no DHCP offer at all** (and an iPXE `exit` as a
second line of defence), so `kubit pxe` can stay running and BIOS boot order
"network first" is safe on a LAN Kubit controls. Unknown machines get Talos while
*Enrollment* (PXE page) is *open*, only known or armed ones when it is *closed*. A member
armed with *Boot into Talos* is served once; discovery clears the arming when it sees
the machine in maintenance mode. Recommended BIOS for the EliteDesks: UEFI only, Secure
Boot off, AHCI, WoL on, and **disk first** unless you own the LAN's DHCP — first
contact via AMT *Boot into Talos*, F9 network boot, or the ISO stick; re-provisioning
never needs PXE because `node remove` resets Talos to maintenance mode from disk.

## Lab hosts (one machine, several Talos VMs)

A machine with AMT can become a **lab host**: Inventory → *Make lab host* (or
the machine page). Kubit arms a network boot (`provision_kind = labhost`, the PXE
process serves the Debian 13 netboot installer with a preseed from
`GET /api/v1/labhost/preseed`), resets the box via AMT, and the unattended install
puts Debian + `qemu-kvm` + `libvirt` on the install disk chosen in the dialog (a
select over the inventory's writable disks, largest preselected, when a Talos scan has
seen the machine; with one or no known disk the dialog states what will happen and the
installer takes the largest non-removable disk) with `br0` bridged onto the LAN and Kubit's SSH key (minted once, sealed in settings) for user `kubit`. The
`labhost.provision` operation waits for SSH, verifies `/dev/kvm` and the bridge,
records capacity and fetches the Talos kernel/initramfs onto the host
(`/var/lib/kubit/boot`). Then **Add VMs…** (count, vCPU, RAM, disk, optional data
disk; memory checked against what is free, host keeps 2 GiB): each VM is a libvirt
domain — `vda` for Talos, a second thin qcow2 `vdb` when a data disk was asked for,
which the lab plan claims as `/var/mnt/data-1` on every node (`vda` stays the install
disk even when the data disk is larger) — that boots Talos
**directly from the kernel/initramfs** — no PXE, no ISO — into maintenance mode, and
is a machine row from the start (source `lab`, MAC `52:54:00:6b:HH:NN`, `host` = the
lab host). They are picked in the wizard like any machine; when the cluster installs a
VM, Kubit flips it to boot from its disk before Talos's post-install reboot. *Make lab host* can carry a plan — VM count and sizes, cluster name and 1 or 3
control planes — so one click runs install → VMs → `cluster.create` (`labhost.cluster`
operation; hostnames `<name>-cp-NN` / `<name>-worker-NN`, no VIP for a single control
plane) and the operator comes back to a running cluster. The lab host page's *VMs*
tab has the VM table (start/stop/re-provision/delete/resize); *Add VMs* and
*Release* live on its Actions tab. Release deletes the VMs, drops the role and leaves
the machine `unknown` (Debian stays on disk); it is refused while installing, in setup
or updating, and Retire is refused until the host is released (a VM row is deleted
from its host, not retired). Lint reports an all-VMs-on-one-host control plane as
`lab-cluster` (info).

### Watching an install, and installing without AMT

The install is not a blind wait any more. The preseed reports each stage back
(`early_command` → `installer`, partman → `partitioning`, `late_command` →
`packages` … `late-done`, a one-shot unit on first boot → `booted`) through the pxe
proxy's `/labhost/<mac>/progress` to `GET /api/v1/labhost/progress`, which lands on
`LabHost.Install` (live on the lab host page). The operation runs
phases with their own budgets and diagnoses — `boot` (PXE saw the MAC, 3 min: else
"check BIOS boot order / AMT override / Wi-Fi interface"), `ipxe` (kernel fetched,
2 min: else "TFTP/HTTP blocked"), `installer` (5 min: else "installer never reached
the network"), `install` (30 min: else "stopped after <stage>; attach a screen"),
`ssh` (5 min: else "booted the old OS / key not installed") — and every PXE log line
about the MAC is mirrored into the operation as it appears. *Boot into Talos* gets
the same `boot` and `ipxe` phases. `internal/api/labhost_install.go`.

A machine without remote management can still become a lab host: *Make lab host*
offers a **manual** plan (`{"manual": true}`) — Kubit arms the row and prints the
kernel, initrd and command line to boot with; you boot it (VM console, USB). For
that, `kubit pxe --http-only [--ip ADDR]` serves just the HTTP side (assets, preseed,
progress) without root; `POST /api/v1/machines {mac, ip, hostname, arch}` registers
a machine Kubit has not seen. `hack/lab/lab.sh` is exactly this on a vfkit VM:
`lab.sh create 1` registers the MAC, calls *Make lab host* (manual, default plan: 4
VMs + cluster `lab`), builds a FAT boot volume with systemd-boot + the netboot
installer + the preseed URL (the installer must run in UEFI mode for partman-efi and
grub-efi; vfkit's own kernel loader is not EFI), and boots it with nested
virtualisation so the Talos VMs get real KVM. The preseed installs per-arch packages
(`qemu-system-x86 ovmf` / `qemu-system-arm qemu-efi-aarch64`), the domain XML carries
the matching UEFI loader (`OVMF_CODE_4M` / `AAVMF_CODE`) so disk boot works on both
arches, and grub also lands on the removable EFI path. `KUBIT_LAB_ALLOW_TCG=1` lets
`setup` continue without `/dev/kvm` (VMs under software emulation) — dev only.

**Sizing.** Every VM gets at least 2 GiB (`minVMMiB`; the dialog and the API both
refuse less): a 1 GiB Talos guest keeps ~450 MiB for pods once Talos and the kubelet
have theirs, and the platform add-ons alone need more — that shape OOM-churned MetalLB
on the EliteDesk and cost the host a core per worker in reclaim. The *Make lab host*
and *Add VMs* dialogs propose what fits (`planFor`): as many 3 GiB VMs as the host
memory minus the 2 GiB reserve allows, else fewer, larger ones (a 7.7 GiB host gets one
control plane and one worker at 2816 MiB), the first being the control plane. Both the
plan path and *Add VMs* refuse a set that exceeds host memory minus the 2 GiB reserve — an
overcommitted host does not fail loudly, its guests swap until kube-scheduler dies
and the cluster sits at NotReady (found in the harness; the memory alert fired, the
refusal is what prevents it). While a manual install waits for its machine, the Lab
host tab shows the kernel, initrd and command line to boot with (copy buttons).

**Routed VM network.** The plan's `network: "routed"` (default `bridge`) puts the VMs
on a Kubit-owned libvirt network instead of `br0`: `kubit`, `192.168.123.0/24`,
dnsmasq DHCP, `<forward mode='open'/>` so libvirt adds no firewall rules of its own
(its NAT mode rejects new inbound connections, which is exactly what Kubit needs to
reach the VMs), plus `kubit-vmnet.service` on the host for `ip_forward` and an
nftables masquerade for egress. Kubit's own host then needs a route to that subnet
via the lab host — the `setup` step logs it. For uplinks that drop frames from other
MACs: Wi-Fi hosts, switch ports with port security, and the vfkit harness (vmnet
filters foreign MACs, which is why `lab.sh` uses it and has `lab.sh route`).

### On this Mac (vfkit)

The Mac running Kubit can be a lab host too: Inventory → *+ Lab host on this Mac*
(shown when the daemon runs on macOS and no such host exists). `GET
/api/v1/labhosts/local` reports what the Mac can give (CPUs, memory, free disk, macOS,
vfkit version) and what is missing, with the brew command; `POST /api/v1/labhosts
{"driver":"vfkit", "vms":…, "cluster":…}` creates the host row synchronously (keyed by
the hardware MAC from `networksetup`, since Go sees en0's private Wi-Fi address; IP
`192.168.105.1`, the vmnet gateway, so the row does not move between networks) and runs
`labhost.local`: `setup` (prerequisites, Talos ISO from the Image Factory into
`~/.kubit/vms/boot/<version>-<schematic>/`) → `define` → `vmboot` → `cluster`, holding
`caffeinate -i`. Needs `brew install vfkit` and vmnet-helper (see *Dev VMs*), macOS 26+.

Driver seam: `labhost.Driver` (`internal/labhost/driver.go`) is what the API, the
cluster manager and the watcher use; `Manager.LabDial` picks it from
`LabHost.Driver` (`""` = Debian/libvirt over SSH, `vfkit` = `internal/labhost/vfkit`).
Debian-only parts sit behind optional interfaces (`Updater`, `Router`) or `LabSSH`, so
updates, reboot, remote management and the install notices do not exist for the Mac
(the routes answer 409).

Each VM is `~/.kubit/vms/<name>/` (`spec.json`, sparse `disk.raw`/`data.raw`,
`efi-vars`, `console.log`, `vfkit.log`) and a launchd job
`dev.kubit.vm.<hash of the VM directory>.<name>` (so a scratch `KUBIT_HOME` never
touches another home's VMs) bootstrapped into `gui/<uid>` from `launchd.plist` in that directory (not
`~/Library/LaunchAgents`, so no login-item prompts): `vmnet-run --operation-mode shared`
(192.168.105.1–.100) → `vfkit` with EFI, virtio disk(s) and net, its REST API on the
unix socket `rest.sock` in the VM's 0700 directory (owner only; the path must stay under
macOS's 104-byte limit, which setup checks), and the Talos ISO as a read-only USB disk
while the VM boots Talos. VMs outlive daemon restarts; Stop is REST `Stop` (guest
shutdown; the operation waits up to 90 s for the VM to be off), force stop is `HardStop`
+ `bootout` and returns once vfkit is gone; spec changes are serialised per daemon; Resize applies at the next start; the disk-boot switch
before the install drops the ISO from the job; Re-provision re-attaches it and wipes
the disk and EFI variables at the next start. `spec.json` `run` is the desired state:
at daemon start, VMs that should run but are not loaded (after logout or a reboot) are
started. Addresses come from `/var/db/dhcpd_leases` by MAC. The lab cluster's MetalLB
range and VIP follow the VM subnet (`.200–.220`, `.250`), reachable from the Mac only.
macOS keeps `max(4 GiB, ¼ of RAM)` (`Capacity.reserveMiB`); the dialog proposes 3 GiB VMs
and cluster `mac`. Release deletes every VM, its files and the Mac's row. `vms/` is
left out of Kubit backups.

Verified on an M4 Pro (24 GiB, macOS 27, vfkit 0.6.4, 2026-09-25): 1 control plane +
1 worker from click to Ready with the platform applied in about 5 minutes (ISO fetch
included), both VMs in maintenance mode 30 s after start; ingress on `192.168.105.200`
answers from the Mac; Talos's post-install and `node reboot` reboots are kexec and keep
the vfkit process; force stop → start boots the installed disk (Ready in 20 s); daemon
restart leaves the VMs running; an unloaded job comes back through autostart; add VM
with data disk (`vda`/`vdb` virtio, the ISO is a read-only USB `sda` and never an
install candidate), re-provision and delete. **Not verified:** a firmware reboot
(`talosctl reboot --mode powercycle`) — the VM probably stops and shows `shut off`
until started; laptop sleep during setup; the daemon under launchd (`kubit service`)
reaching the VM subnet through macOS Local Network privacy.

### Host metrics, alerts and updates

The host itself gets the treatment nodes get. Every service interval the watcher's
SSH tick also reads `/proc` and `df` (`labhost.Client.Metrics`: load, CPU %, memory
used, the filesystem carrying `/var/lib/kubit`, running VMs, uptime) and files a
sample under the pseudo-cluster `labhost:<mac>` in the same `samples` table
(`disk`/`disk_cap` columns, migration v11) — so the lab host page shows CPU, memory,
VM disk and VM count with the same sparklines and ranges as a cluster overview, and
a `hostSample` live message appends each reading. Alerts come from the same path
(`events` under `labhost:<mac>`, runbooks, Inventory pill, forwarders):

| kind | raised | cleared |
|---|---|---|
| `labhost.disk-low` | VM filesystem ≥ 85 % (warn), ≥ 95 % (critical; the warn is resolved and re-raised) | `labhost.disk-ok` below 80 % |
| `labhost.memory-pressure` | ≥ 92 % used for three readings | `labhost.memory-ok` below 85 % |
| `labhost.unreachable` | three failed SSH ticks in a row | `labhost.back` |
| `labhost.updates` (info) | pending packages or a reboot required, at most daily | — |

Thin-provisioned VM disks are why the disk alert exists: the host's filesystem
filling up pauses every VM at once. Hourly the tick also refreshes apt (`CheckUpdates`:
pending count, security count, `/var/run/reboot-required`, running vs newest
installed kernel, release, whether unattended-upgrades is on); *Check now* does it on
demand. The preseed installs `unattended-upgrades`, so Debian security and stable
fixes land daily on their own, never with a reboot.

**Update host** (`labhost.update`) is the reboot: `check` → `upgrade` (apt
`full-upgrade` + `autoremove`, non-interactive, config files kept) → if no reboot is
needed the operation ends there and the VMs were never touched → otherwise `vms`
(graceful `virsh shutdown`, 90 s, then destroy) → `reboot` (wait for SSH, up to
10 min) → `resume` (domains are `virsh autostart`, stragglers started) → `cluster`
(every member node on this host Ready). **Reboot host** (`labhost.reboot`) is the same
without the upgrade. Both refuse while the host is not `ready`, are gated by the
maintenance window of every cluster that has members on the host (409 + `ignoreWindow`
like other disruptive routes), are recorded against that cluster so the workload
quiet window applies, and the watcher skips the host while it is `updating` so the
reboot is not counted as an outage. The heartbeat lists each lab host with its disk
percentage and pending updates.

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
`~/.kubit` minus `bin/`, `cache/`, `vms/` and `.terraform/`, sealed with the master key
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
- [x] Phase 8 — `kubit pxe` (proxyDHCP + TFTP + HTTP; verified 2026-09-16 on an HP EliteDesk 800 G3: AMT-forced UEFI PXE → iPXE → Debian installer, 7 min to a ready lab host)

Verified on vmnet-helper VMs: 3-control-plane create with a VIP (etcd 3/3 in 20 s, API
via the VIP, platform applied), `node add` worker and control plane, `node remove`
worker and — with `--force` — a control plane (graceful etcd leave, membership 3→2, node
back in maintenance mode), quorum guard refusing 3→2 without `--force`.

Not yet exercised: `runsc-kvm` (no nested virtualisation in the VMs).
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
- [~] M14 — Out-of-band: Intel AMT and Redfish backends (probe, power on/off/reset/cycle, one-shot PXE boot; Redfish also reports CPUs, memory and drives before Talos), per-machine remote-management config sealed at rest with a type selector, *Add by remote management* in Inventory, default BMC credentials in settings, discovery finds Redfish roots, `machine.power` operations; member-aware PXE (no offer + iPXE exit for members, `/pxe/decide`, enrollment open/closed, one-shot arming cleared on maintenance sighting; unit-tested). **Redfish is unverified on a real BMC** (fake-BMC tests only); AMT verified on an EliteDesk 800 G3
- [~] M15 — Lab hosts: `internal/labhost` (preseed, SSH client, virsh domain lifecycle, direct kernel boot), PXE Debian profile + preseed proxy, lab-host API/operations, watcher refresh, install-time disk-boot switch, wizard/machine-page/Inventory UI. **Unverified on hardware** (needs the EliteDesk): the Debian install and every virsh call; unit-tested rendering only
- [~] M16 — Lab host operations: host metrics (SSH tick → `samples` under `labhost:<mac>`, live `hostSample`), disk/memory/unreachable/updates alerts with runbooks and hysteresis (unit-tested), hourly apt check, unattended security upgrades in the preseed, `labhost.update` / `labhost.reboot` operations (VMs parked, autostart, cluster Ready wait, maintenance-window gate), *Lab host* tab with utilisation cards, System panel and confirm dialogs, Inventory alert pill, heartbeat line. **Verified with a seeded host only** (`hack/seedlab`): parsing, thresholds, UI, the failure path of the operation; the real upgrade/reboot path needs the EliteDesk
- [x] M17 — Disk roles: `dataDisks` per node → Talos `UserVolumeConfig` whole-disk xfs volumes at `/var/mnt/data-N` (generation and validation unit-tested), wizard Design step and add-node dialog with per-disk checkboxes, node page mounts, lab VMs with an optional second qcow2 (`vdb`) that the lab plan claims automatically, `vda` pinned as the install disk for VMs. Verified on lab VMs (2026-09-26), which found that Talos rejected the old selector's `!system_disk` for whole-disk volumes and failed every data volume; the selector is now the device path alone, and a test evaluates it the way Talos does
- [~] M17 — Lab install observable: installer progress reports, phased waits with diagnoses, PXE log mirrored into operations, manual (no-AMT) mode, `kubit pxe --http-only`/`--ip`, `POST /machines`, per-arch preseed packages, UEFI loaders in domain XML, `hack/lab/lab.sh` vfkit harness (EFI via systemd-boot volume, nested virt). routed VM network (`kubit` libvirt network + masquerade unit). **Verified in the VM harness**: EFI install via systemd-boot volume (3 min), every progress stage, SSH, setup with nested KVM, four Talos VMs to maintenance mode on the routed network, cluster `lab` Ready with MetalLB/ingress in 9 minutes, and M16's *Update host* (VMs parked, reboot, autostart, 4/4 Ready again in 2 min). **Verified on the EliteDesk (2026-09-16)**: AMT one-shot PXE, every phase with the PXE log mirrored, Debian installed and SSH-ready in 7 min, three bridged Talos VMs to maintenance mode; the VM plan has to fit the real host (7.7 GiB RAM). Bridged VMs get no address from libvirt (no leases, no ARP until the host talks to them), so the VM wait sweeps the discovery subnets and matches Talos nodes by MAC. See NOTES/backlog for what was found
- [x] M18 — Machine kinds: `Machine.Kind()` derived from stored fields and sent with every row; node endpoints, PXE decision, watcher and the provision/release/retire/power handlers refuse by kind with one-line reasons; machine page, Inventory, wizard, Add node, palette and Remote management render by kind (`web/src/machine.tsx`). Lab-host install disk selectable in the dialog and pinned in the preseed. Unit-tested (kind table, endpoint refusals, closed port, PXE decision, migration) and checked in the console against a seeded set of every kind; the real lab host page opens on its Debian facts
- [x] M18 — Identity: local accounts with viewer/operator/admin roles enforced per route, sessions and API tokens, first-admin setup, OpenID Connect sign-in with group→role mapping, audit actor, cluster `spec.auth.oidc` → API server `AuthenticationConfiguration` + admin group binding. Unit-tested end to end (fake IdP); **unverified against a real provider**
- [x] M19 — Storage: Longhorn platform add-on on data disks (node labelling in the generator, privileged namespace, replica default from the data-disk node count, wizard/Add-ons/Storage-tab hooks). Enabling it on an existing cluster re-images the nodes through **Upgrade Talos** (extension changes now produce a new schematic, even at the same version, and the upgrade re-applies machine configs first); platform runs refuse until then. Verified on a lab cluster on this Mac
- [x] M19 — Honest health and right-sized labs: `hub.since(0)` replays nothing and replayed messages never toast; service alerts raise after two and clear after three collections; `status.health` (`healthy` / `degraded` / `down`) drives the cluster pill; `node.memory-small` alert with runbook; 2 GiB floor for every lab VM with host-fitting defaults; `worker-undersized` lint and preflight floor when add-ons are on; MetalLB layer-2 only with resource requests on every add-on and `atomic` releases; 2 s host CPU sample. Unit-tested (hub, tracker flap, Derive, lint, tofu golden, validate); the EliteDesk lab reshaped to 1 CP + 1 worker at 2816 MiB and re-applied without FRR
- [x] M22 — Full lab run and GitOps (2026-09-26): from an empty `~/.kubit`, *Lab host on this Mac* (1 control plane + 2 workers with data disks) to a Ready cluster in 5 min 15 s; cert-manager and Argo CD (with `kustomize.buildOptions: --enable-helm`, `timeout.reconciliation: 60s`) in 54 s; Longhorn after a same-version re-image. Apps from [kubit-apps](https://github.com/mikaelhug/kubit-apps): Terraform creates the root Application, an ApplicationSet deploys each `apps/<name>/` (Helm chart via kustomize or plain YAML); four apps served over HTTPS with cert-manager certificates, a Longhorn volume survives pod replacement, a pushed change lands in 1–2 min, a deleted folder is pruned with its namespace. Walked every console view; fixes: stale "Last seen" (now the watcher's last contact), services without a health check shown as unhealthy, finished operations shown with skipped steps as incomplete, upgrade hint pointing at Settings, first-run screen without *Lab host on this Mac*
- [x] M23 — App secrets (2026-09-26): SOPS + age with a per-cluster key held by Kubit (sealed, outlives the cluster, backed up), installed as `argocd/kubit-sops-age` on every platform apply, KSOPS in the Argo CD add-on, recipient/export/import in Add-ons and `kubit sops`. Argo CD's admin password no longer stored. Verified on `lab`: [kubit-apps](https://github.com/mikaelhug/kubit-apps) `apps/linkding` with an encrypted admin login synced 78 s after push and the login works; with Kubit stopped, a deleted app Secret came back decrypted from Git in 3 s; a deleted cluster key came back unchanged on an empty-plan apply
- [x] M24 — Flux replaces Argo CD (2026-09-26): headless `flux2` 2.19.1 add-on, GitRepository and Kustomization from `platform.flux.repository` (prune, `deletionPolicy: Orphan`, SOPS via `flux-system/sops-age`), Argo CD and KSOPS removed, stale templates removed on render, Flux card with live sync state (`GET …/flux`, `flux` scope from CRD-gated informers). Verified on a rebuilt `lab` on this Mac (1 control plane + 2 workers with data disks, Ready in 4 min): cert-manager and Flux applied in 36 s; [kubit-apps](https://github.com/mikaelhug/kubit-apps) synced within seconds of the apply (GitRepository, Kustomization, the podinfo HelmRelease Ready); podinfo, whoami and it-tools served over HTTPS; linkding's Secret decrypted from Git with the key that outlived the old cluster, and came back 484 s after being deleted (the Kustomization's 10 min interval); Flux uses 105 MiB in 4 pods; disabling Flux removed the controllers and the key and left the apps running, re-enabling resumed the sync; a requested reconcile reached the UI as a `flux` refresh within a second. Five simulated users then pushed to kubit-apps from separate clones (concurrent pushes, rebased on rejection): a plain YAML app, a chart from an OCI registry, nginx basic auth from a SOPS Secret encrypted with public recipients only, a malformed Deployment, an update plus a removal. Fixes from that run: one Flux Kustomization per app (the single root held every push back behind the malformed one), `flux.not-ready` alerts, OCIRepository and HelmRepository on the card (a missing chart tag was invisible). After the fixes a broken app fails alone and alerts, the others land within a minute of the push, a password rotation takes effect without restarts, removals prune their namespace. Defaults then changed to a ready-to-go cluster (Flux, cert-manager, Longhorn on data disks; gVisor opt-in; apps repository in the lab dialog and wizard): `lab` rebuilt from the dialog with kubit-apps reached Ready with every add-on in 5 min 33 s, no re-image, and all 14 Flux objects were Ready 21 s later; every app served, Longhorn volumes Bound, health `healthy`
- [x] M25 — Storage on the system disk, in-cluster builds, a blank-canvas run (2026-09-26): `storage.systemDisk` caps EPHEMERAL at `ephemeralSize` (40 GiB) and gives the rest of the install disk to a `data-system` Longhorn volume; the Builds add-on (registry on the last MetalLB address, rootful BuildKit, Talos `RegistryMirrorConfig` `registry.kubit`) with build Jobs in the apps repo; forgetting a cluster resolves its open alerts; Flux objects waiting on a dependency never alert. Verified from an empty `~/.kubit` and a new master key: the lab host dialog (1 control plane 3 GiB, 2 workers 2 GiB, 60 GiB disks, no data disks, kubit-apps `./flux`) gave a Ready cluster with every add-on in under 5 min, each node a 17 GiB `data-system` Longhorn disk, the SOPS key installed and the first sync done. The first run found Longhorn's 30 % default-disk reserve leaving 12 GiB schedulable, so the 20 GiB registry volume never scheduled; reserve now 5 %, registry 5 GiB. Five simulated users then pushed from their own clones: Ben's Deno clock (a single `main.ts`, edits roll out by themselves), Eve's bot without a Service (secret from SOPS, calls Ben's clock over cluster DNS), Dan's docker-compose WordPress + MariaDB (DB passwords from SOPS, Longhorn volumes), Cara's web/api/worker monorepo with Redis (image built in the cluster in 12 s) and Ana's Phoenix + Postgres (built in the cluster in 43 s; 0.1.1 with a wait for Postgres re-created the build Job and rolled out). All served over HTTPS, data survived deleting database pods, 4.4 of 6.6 GiB RAM in use, health `healthy`

- [x] M21 — Namespace scopes: Apps · Platform · All with a namespace picker on Workloads, Network and Storage (URL-backed, live on namespace changes), Overview counts app and platform pods apart, CronJobs listed. Verified on a lab cluster on this Mac: fresh cluster opens on an empty Apps (14 platform pods), a new `shop` namespace appears live, `?ns=` links filter Network and Storage
- [x] M20 — Lab host drivers: `labhost.Driver` seam (libvirt unchanged), *Lab host on this Mac* with vfkit + vmnet-helper VMs under launchd, driver-aware lab host page, per-host memory reserve. Verified end to end on this Mac (see *On this Mac*); firmware reboot, sleep during setup and the launchd-run daemon are not. Hyper-V: feasibility only, in NOTES/backlog.md. Full-stack run on this Mac (2026-09-25): three control planes (4 GiB, data disks) from the dialog to Ready with MetalLB, ingress, gVisor and metrics-server in 3 min 35 s; cert-manager and Argo CD applied from the Add-ons tab in 51 s; an Argo CD Application (podinfo from GitHub) synced and served over HTTPS through ingress with a cert-manager certificate; a gVisor pod ran; a control plane killed and restarted kept the API up on the VIP and raised and auto-resolved `talos.unreachable`. Longhorn not exercised
- [~] M7 — tests and packaging: gofmt/vet/race tests and `hack/e2e.sh` run locally; GitHub Actions (CI, signed releases, nightly e2e lab) removed as unused. The QEMU lab script (`hack/qemu/lab.sh`) stays for a Linux KVM box, **unverified**
