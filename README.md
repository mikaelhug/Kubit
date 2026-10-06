# Kubit

Declarative provisioner for Talos Linux clusters on bare metal and VMs. One Go binary:
CLI, a foreground daemon (health, alerts, discovery) and a web console that edits the repo.

Since 2026-10-05 Kubit is being reshaped from a click-ops console into an IaC tool: the
cluster's desired state lives in a git repo you choose, `kubit plan` / `kubit apply`
converge it, and the console writes the same `cluster.yaml` and runs the same plan/apply.
You commit and push. The roadmap is under *Status*.

## Architecture

| Layer | Owner | Mechanism |
|---|---|---|
| Discovery, machine config, apply, bootstrap, kubeconfig, OS/K8s upgrades, drain, reset, etcd | Kubit | Talos API via `siderolabs/talos/pkg/machinery` + `client-go` |
| Platform add-ons: MetalLB, Traefik, gVisor, metrics-server, cert-manager, Longhorn, Flux, Builds | OpenTofu (`internal/tofu/templates/platform/`), run by Kubit with a pinned binary and locked providers | `hashicorp/helm`, `alekc/kubectl` |
| User workloads | Flux (headless), syncing an apps repository you own | GitRepository + Kustomization `flux-system/flux-system` from `platform.flux.repository` |
| App secrets | SOPS files in the apps repository, one age key per cluster | kustomize-controller decrypts in the cluster |
| Escape hatch | You | `sops -d secrets.sops.yaml` plus `kubit talosconfig`/`kubeconfig`: plain talosctl and kubectl work without Kubit |

A cluster lives in a repo directory you choose (*Cluster repo*). Kubit keeps no state of
its own: every run rebuilds what it needs from the repo and the live cluster. `~/.kubit`
holds only disposable caches (the tofu binary, the rendered platform module, PXE assets).

## Layout

```
cmd/kubit/   CLI (cobra)
internal/    api, cluster, config, factory, k8s, pxe, store, talos, tofu, watch, …
web/         Vite + Preact + TypeScript + Tailwind; dist/ embedded via go:embed
hack/vm/     vfkit harness: Talos arm64 VMs on Apple Virtualization.framework
hack/qemu/   QEMU/KVM lab for a Linux box (unverified)
```

## Build

```
make build   # web + go build → bin/kubit
make test
```

Go 1.26+, Node 20+. Talos machinery v1.14.2 (Kubernetes 1.37.1 default). Credentials
never belong in this repo: `.gitignore` covers kubeconfig, talosconfig, `secrets*.yaml`,
keys and tfstate, and `TestRepoHoldsNoCredentials` fails on any tracked one.

## Dev VMs

`brew install vfkit nirs/vmnet-helper/vmnet-helper`, then `hack/vm/vm.sh create 1`
(2 vCPU / 4 GiB / 20 GiB, boots the Talos ISO), `list`, `start 1 --no-iso`,
`destroy all`. VMs run under `vmnet-run` with isolation off so they share
192.168.105.0/24 with the host; vfkit's own NAT isolates VMs from each other and breaks
etcd and the VIP. The Talos API is flaky for about two minutes after boot.

`hack/scenario.sh` drives a daemon's API through a whole cluster life on vm1–3: discover,
create a repo, a stale edit refused, create, add a worker, pin it static, move it, move the
control plane with the endpoint, release to DHCP, upgrade Talos and Kubernetes, toggle an
add-on, an etcd snapshot, remove a node. Every step plans, applies, and checks that the next
plan is empty, the Lease is free and the nodes are Ready. `WORK=<dir>` keeps the state,
`START=<n>` resumes at step n.

## Cluster repo

```
lab/
  cluster.yaml         the declaration; Kubit never writes it after init
  secrets.sops.yaml    Talos secrets bundle, Flux age and deploy keys, state passphrase, backup S3 keys
  .sops.yaml           age recipients for *.sops.yaml
  snapshots/           etcd snapshots, age-encrypted (git-ignored)
  .gitignore           talosconfig, kubeconfig, .terraform/, snapshots/
```

`kubit init lab --nodes 192.168.1.0/24` probes the machines in maintenance mode,
proposes `cluster.yaml` for them (`config.Design`: bare-metal control planes first, a VIP
in their /24, metrics-server, cert-manager and Flux; `--lb-range` adds MetalLB and
Traefik) and writes the
secrets SOPS-encrypted for the recipients in `.sops.yaml`, `--age`, or your own age key
(created at the sops default path when you have none; back it up). It never overwrites.

Kubit reads and writes SOPS files itself (`internal/sops`: AES-256-GCM values, age-wrapped
data key, MAC), compatible with the `sops` CLI both ways, so `sops lab/secrets.sops.yaml`
edits the same file. Keys come from `SOPS_AGE_KEY`, `SOPS_AGE_KEY_FILE` or the sops
default `keys.txt`.

**Moving a cluster in.** A cluster from before the repos lives in `~/.kubit/kubit.db`.
Build commit `dbeba6f` and run `kubit export lab --repo ~/lab` with it once; later
builds do not read that database.

Credentials are derived, never stored: `kubit talosconfig lab` signs a one-year admin
client certificate with the bundle's OS CA, `kubit kubeconfig lab` one for
`system:masters` with the Kubernetes CA (`-o file` to write it, 0600).

## cluster.yaml

```yaml
apiVersion: kubit.dev/v1
kind: Cluster
metadata: { name: dev }
spec:
  talosVersion: v1.14.2            # default: machinery's version
  kubernetesVersion: v1.37.1
  extensions: []                   # default: derived from gvisor and longhorn
  controlPlane:
    vip: 192.168.64.9              # optional Layer-2 VIP
    endpoint: https://192.168.64.9:6443   # default: VIP, else first control plane
    allowScheduling: true          # default: true below 6 nodes
  network:
    podCIDR: 10.244.0.0/16
    serviceCIDR: 10.96.0.0/12
    nameservers: [192.168.64.1]
    ntp: [time.cloudflare.com]
    policies: true                 # NetworkPolicy enforcement (default true)
    discovery: true                # discovery.talos.dev (default true)
  storage:
    systemDisk: true               # rest of the install disk → data-system for Longhorn
    ephemeralSize: 40GiB
  patches:                         # Talos strategic-merge patches, every node
    - machine: { sysctls: { vm.max_map_count: "262144" } }
  nodes:
    - hostname: cp-01
      ip: 192.168.64.2
      mac: "52:54:00:4b:49:01"
      role: controlplane           # default: worker
      arch: arm64
      kvm: true
      installDisk: { path: /dev/vda }
      dataDisks: [/dev/vdb]        # whole disks → xfs at /var/mnt/data-N
    - hostname: gpu-01
      ip: 192.168.64.150
      labels: { workload: gpu }
      taints: { nvidia.com/gpu: "true:NoSchedule" }
      installDisk: { selector: { minSize: 100GB, type: nvme } }
      network: { addresses: [192.168.64.150/24], gateway: 192.168.64.1, vlan: 0 }
      patches: []                  # this node only, after the cluster patches
  backup:                          # absent = off
    schedule: "0 */6 * * *"
    s3: { bucket: talos-backups, region: eu-north-1, endpoint: "https://s3.example.com", prefix: lab, pathStyle: true }
    ageRecipients: [age1…]         # who can open the snapshots
  platform:
    metallb: { enabled: true, range: 192.168.64.200-192.168.64.220 }
    traefik: { enabled: true }
    gvisor: { enabled: false }
    metricsServer: { enabled: true }
    certManager: { enabled: true }
    longhorn: { enabled: true }
    builds: { enabled: false }
    flux:
      enabled: true
      repository: { url: ssh://git@github.com/you/apps.git, branch: main, path: ./flux }
```

The Discovery page copies a node entry for any machine in maintenance mode.

**Addresses.** `ip` is where Kubit finds the node now; `network` is what Kubit configures.
A node without `network` keeps DHCP. `network.addresses[0]` is the node's target: when it
differs from `ip`, the next apply moves the node there and rewrites `ip:` once the node is
Ready at the new address (commit that change). Validation: a static node needs a gateway
inside its prefix that is not its own address; no network or broadcast address; no address
owned by two nodes (across `ip` and targets); the VIP and the pod and service CIDRs stay
clear; without a VIP, `controlPlane.endpoint` must follow a control plane that moves.

**Generated configs** are Talos 1.14 multi-document: v1alpha1 core,
`UnattendedInstallConfig` (installer + disk selector), `HostnameConfig`,
`KubeNodeConfig` (node labels, taints), `LinkAliasConfig` `uplink` by MAC,
`DHCPv4Config` or static `LinkConfig` + `RouteConfig` (+ `VLANConfig`),
`ResolverConfig`, `TimeSyncConfig`, `Layer2VIPConfig`, Flannel with
kube-network-policies, `DiscoveryServiceConfig` and, with `spec.auth.oidc`, a JWT
authenticator for kubectl SSO. One image (schematic) serves every node. Patches apply
last (cluster → node) and every plan validates the generated
configs with Talos's metal-mode rules. `config.Lint` reports advisory findings.
Talos < 1.14 is rejected.

**Disks.** `installDisk` holds Talos; each `dataDisks` entry becomes a whole-disk xfs
`UserVolumeConfig` at `/var/mnt/data-N`. With `storage.systemDisk` a node without data
disks keeps `ephemeralSize` for `/var` and gives the rest of the install disk to
`data-system` (fresh installs only; Longhorn must be on).

**Not in cluster.yaml.** Host firewall, disk encryption, watchdog and per-pool images are
Talos patches (`NetworkRuleConfig`, `VolumeConfig`, `WatchdogTimerConfig`). A removed
field fails the parse with its replacement.

## State

Kubit keeps no state of its own. Each run reads:

| What | From |
|---|---|
| Declaration | `cluster.yaml` |
| Talos secrets, Flux keys, state passphrase, backup keys | `secrets.sops.yaml`, decrypted in memory |
| Add-on state | the cluster: Secret `kube-system/tfstate-default-kubit-platform`, encrypted by tofu |
| etcd snapshots | `snapshots/*.db.gz.age` (git-ignored) |
| Versions, members, schematics, config drift | the live nodes (Talos dry run) and the Kubernetes API |

A half-finished create resumes from what the nodes report. Machines found by discovery,
alerts and samples live in the daemon's memory and start empty.

**App secrets (SOPS + age).** Each cluster has an age identity in its
`secrets.sops.yaml` (`flux.ageKey`), so a rebuild from the repo decrypts again. Every
platform apply with Flux installs it as `flux-system/sops-age`, and the root
Kustomization decrypts with it. New repos encrypt app secrets to your own key plus the cluster's recipient
(shown with *Copy* on the Add-ons tab's Flux card); see *Secrets editor*.

**Private apps repo (deploy key).** `platform.flux.repository.url` is an `https://` URL
for a public repo or an `ssh://user@host/path` URL for a private one (the scp form
`git@host:path` is refused; Flux takes only ssh:// URLs). For ssh, *Generate* on the Flux
card (or `kubit deploy-key lab`) writes a new ed25519 key to `secrets.sops.yaml`
(`flux.deployKey`, under the rule for your keys only) together with the host's keys
(`flux.knownHosts`, every type the host offers; compare the fingerprints with the host's
published ones). Add the public key to the repository's deploy keys, read-only; on GitHub one
deploy key serves one repository. Every plan then connects with the key and lists as a
problem a refused key, a host key not in `flux.knownHosts`, a missing repository or branch,
and an https repository that is private or missing; an unreachable host adds nothing. The
platform apply installs the key as Secret `flux-system/flux-system` (`identity`,
`known_hosts`) and points the GitRepository at it. To rotate: *Replace*, add the new key on
the host (the plan stays blocked until then), apply, remove the old key. *Rescan* (or
`--hosts`) re-reads the host keys after the host rotates them.

**When Flux syncs.** source-controller asks the repository for the branch head every
`platform.flux.repository.interval` (default 1m) and fetches only when it moved;
kustomize-controller applies a new revision as soon as it arrives and re-applies every 10m
to undo drift. Both run in the cluster and pull, so nothing inbound is needed and the laptop
may sleep; a failure shows as the GitRepository's message on the Flux card.

## Discovery

Discovery → Scan probes :50000 and reads the insecure
maintenance API: MAC of the uplink, arch, CPUs, RAM, disks, KVM, SMBIOS
UUID and serial. Machines are keyed by MAC, so a new DHCP lease keeps the row and shows
the new address next to a member's declared one. Kinds are derived: **member** (in a cluster),
**maintenance**, **configured** (Talos with a config Kubit did not apply), **booting**,
**unbooted**. Every minute the daemon scans its own /24 (from the default route), `discoverySubnets` and
the /24 of every node declared in a served repo for new maintenance-mode machines and
reprobes the known ones. Every 10 seconds it checks :50000 on every machine that is not a
cluster member: two misses in a row mark it `offline`, and an offline machine that answers
again is reprobed. Changes are pushed to the console.

From discovery to a cluster, in the console:

1. **Discovery** lists the machines that can be added (maintenance mode, in no served
   `cluster.yaml`), the subnets it scans and the last scan; another subnet or address
   can be scanned on demand. Select the machines and choose *Add*.
2. **Choose** a served cluster or *New cluster* (a repo directory and a name) and a role
   per machine; *auto* picks 1 control plane, or 3 from three machines. The name is a
   lowercase DNS label of at most 50 characters (it prefixes every hostname); the default
   is the directory name made into one. Control planes are listed first in `cluster.yaml`.
3. **Review** shows what will be written before anything is: hostnames, addresses, roles,
   install disks, the Talos and Kubernetes versions and, for three control planes, the
   VIP. Kubit suggests a VIP between .250 and .240 of the nodes' /24 that answers on none
   of 6443, 50000, 22, 53, 80, 443 and 445, and refuses to write one that does. Design
   warnings (single control plane, undersized machines) are listed. The review appears at
   once; the VIP and address checks run in one parallel round behind it, and *Write* is
   enabled when they pass.
4. **Write** creates the repo (`cluster.yaml`, `secrets.sops.yaml`, `.sops.yaml`,
   `.gitignore`, `git init` when the directory is in no git repository) or appends the
   nodes, and opens the cluster's Changes tab, where the plan is already running. The machines move
   to *Waiting for apply* on Discovery.
5. **Plan** lists every install with the disk it erases. A declared node is accepted only
   when the machine answering at its address has the declared MAC; a node whose lease
   moved is found by MAC on its /24.
6. **Apply** states the machines and disks in its confirmation, then streams the log.
   The Mac does not idle-sleep while an apply runs (`caffeinate`). Members leave
   Discovery's lists as they install.
7. **Repository › Git** shows the branch, uncommitted files and unpushed commits, live
   from git; commit and push from your own tools.

## Plan and apply

`kubit plan lab` compares the repo with the live cluster and prints what `kubit apply lab`
would do:

```
+ add                w-03: worker at 192.168.1.23
~ config             w-01
      ~ machine.network.nameservers …
~ upgrade talos      v1.14.0 → v1.14.2
- remove             w-09: drain, delete and reset (needs --allow-removal)
~ platform           traefik: 1 to update
lab: 5 change(s). Plan 6533c580b90d08e086e949f3.
```

It validates every generated node config with Talos's metal-mode rules, checks that a
new cluster's VIP is free, then observes:
each declared node over the Talos API with the repo's credentials (member) or the
insecure maintenance API (to join; found by MAC when its lease moved), the Kubernetes
nodes, the installed Talos version and schematic, a Talos **dry-run apply** of each
member's regenerated config (the diff is Talos's own), and `tofu plan` of the add-ons.
Nothing is changed. `--detailed-exitcode` exits 2 when there are changes; problems
(unreachable nodes, a declared address that moved, the API down) exit 1.

`kubit plan` never writes (`tofu plan -lock=false`). `kubit apply --plan <hash>` applies only
if the plan still has the reviewed hash, for CI that plans on a pull request and applies on
merge.

`kubit apply` takes a Kubernetes Lease `kube-system/kubit-apply` (one apply per cluster
at a time, expires after an hour), plans again, asks (or `--yes`), then converges in
this order. The Lease names its holder (`console/<host>/<pid>`, `cli/<host>/<pid>`); a Lease
held by a process on this machine that no longer runs is taken over instead of waited out.

1. **address** (below), before anything else.
2. **create** when no node is a member yet: preflight → schematic → configs from the
   repo's secrets → install in parallel → bootstrap etcd → nodes Ready. Re-running
   resumes an interrupted create.
3. **add** each declared machine in maintenance mode.
4. **config**: dry-run first, apply only where Talos reports a diff, wait for Ready.
5. **upgrade talos**, then **upgrade kubernetes**, node by node, control planes first.
6. **remove** members no longer declared, only with `--allow-removal` (drain → delete →
   graceful reset; refuses the last control plane, the no-VIP endpoint, or two left).
7. **platform**: `tofu apply` of the add-ons, state in the cluster (below).

**Address changes** plan as `address` rows (`cp-01  192.168.5.240 → 192.168.5.51`,
`DHCP → static`, `→ DHCP`, a changed prefix, gateway or DNS) and, when the endpoint follows,
an `endpoint` row. The plan refuses: a target that already answers on the network, a
control-plane move with exactly two control planes, releasing the endpoint control plane to
DHCP without a VIP, and an endpoint change without a control plane moving to it. Apply
moves one node at a time (other control planes, then the endpoint holder, then workers),
after a `pre-move` etcd snapshot:

1. Talos **try** apply at the old address: Talos restores the previous config by itself
   after 3 minutes unless confirmed.
2. Find the node at the new address (by hostname when it is DHCP), then confirm with a
   no-reboot apply of the same config.
3. Pin the new endpoint, reboot at the new address (proves the config persists).
4. Control planes: update the node's etcd peer URL (Talos never does) through the etcd
   gateway on :2379 with a 10-minute client certificate from the cluster's etcd CA.
5. Wait for Ready with the new InternalIP, then write `ip:` in cluster.yaml.

Other nodes then get the new endpoint through the normal config step. A move that needs a
reboot for other reasons is refused: apply those changes first. An interrupted run resumes
from the plan (`record` writes a missing `ip:`, `repair` fixes a stale etcd peer URL).

**Node lost at both addresses.** Wait 3 minutes: an unconfirmed try apply rolls back to the
old address. A confirmed node that answers nowhere needs a console: boot it, read its
address, set `ip:` to it and plan again. A single control plane that does not come back is
restored from the `pre-move` snapshot (*Backups*).

Kubit's client certificates (talosconfig, kubeconfig, the etcd peer repair) start 24 hours in
the past, so a node whose clock lags still accepts them.

Waits on nodes (Ready, a new boot, a kubelet version, a moved InternalIP) list the Node
objects once and then follow a watch on them, reconnecting while the API is down; they react
to the change instead of a poll interval.

A second `kubit apply` right after finds nothing to do. `~/.kubit` is only a cache: Kubit
rebuilds its view of a cluster from the repo and the live nodes on every run, so a fresh
machine with the repo and the age key plans the same way. A cluster cached in `~/.kubit`
with other secrets is refused.

**Upgrades** run per node, control planes first: pull the installer, stage it, check etcd health (never with exactly two members), drain (5 min, PDBs respected),
reboot, wait for Ready and the etcd member count, uncordon. A changed extension set
re-images even at the same version. Every upgrade starts with prechecks (API, etcd,
nodes Ready, free `/var`, published target, supported version pair, deprecated API use)
and a `pre-upgrade` etcd snapshot.

CI: a workflow can run `kubit plan --detailed-exitcode` on pull requests and
`kubit apply --yes` on merge, with the age key in `SOPS_AGE_KEY` and a runner on the
cluster's network. `hack/e2e.sh <subnet>` runs init → apply → a second, empty plan.

## Platform layer (OpenTofu)

Every plan and apply renders the embedded templates plus `terraform.tfvars.json` and a
kubeconfig into a fresh private temporary directory, runs `tofu init/plan/apply` with a
pinned, checksum-verified binary, and deletes the directory. Nothing carries over between
runs.

- **State** lives in the cluster it describes: OpenTofu's `kubernetes` backend, Secret
  `kube-system/tfstate-default-kubit-platform`, locked by the Lease
  `lock-tfstate-default-kubit-platform`. It is encrypted by OpenTofu (`TF_ENCRYPTION`:
  pbkdf2 key from `platform.statePassphrase` in the repo's secrets, AES-GCM for state and
  plan, no unencrypted fallback). Nothing to commit, never stale on another clone, and
  gone with the cluster it describes. Templates keep the state small (no manifest
  bundles), well under the 1 MiB Secret limit.
- **Providers** are locked by `.terraform.lock.hcl`, shipped with the templates for
  darwin and linux on amd64 and arm64; `init` runs with `-lockfile=readonly`, so every
  machine and CI runner uses the same provider builds. `make tofu-lock` regenerates it
  after a constraint change. Downloads are shared in `~/.kubit/plugins` (OpenTofu's plugin
  cache, `init` serialised by a file lock).
- **Older repos** with `state/platform.tfstate`: *Plan* shows "move state/platform.tfstate
  into the cluster" and plans from that file without locking it; *Apply* moves it
  (`init -migrate-state`), deletes the file, and the log asks you to commit the removal.
  A repo file next to a state already in the cluster is stale and is removed the same way.
- The Add-ons tab reads each chart's Helm release (revision, status, chart and app
  version) from the cluster, not from the state.
- The Traefik address pin is read from the live Service (its MetalLB annotation, else its
  address), so a failed apply never loses it.

Charts are pinned in `internal/tofu/render.go`. Notes:

- `metallb-system` and `longhorn-system` are `privileged`; MetalLB is layer-2 only (FRR
  off); control planes keep LoadBalancer announcements when they run workloads.
- Every add-on has resource requests; releases are `atomic`.
- **Traefik** replaces ingress-nginx: IngressClass `traefik` (default) plus a
  compatibility class `nginx`, Gateway API CRDs v1.6.1 shipped with Kubit as a local Helm
  chart (`gateway-api` in `kube-system`) whose CRDs carry `helm.sh/resource-policy: keep`,
  so turning Traefik off never deletes Gateways or routes. The old
  ingress address is pinned during the migration.
- **Longhorn** runs only on data disks or `data-system` (labelled at machine-config
  time), reserve 5 %, replicas = min(3, storage nodes). Enabling it adds the
  iscsi/util-linux extensions; `kubit apply` re-images the nodes before the platform.
- **Builds**: in-cluster `registry:3` + rootful BuildKit in `kubit-builds`; nodes pull
  `registry.kubit/<app>` through a fixed ClusterIP mirror. Needs Longhorn.
- **Flux**, headless (source, kustomize, helm, notification). One Flux Kustomization
  per app keeps a broken app from blocking the rest.

`platform.<addon>.values` is passed to Helm only when set.

## Console and API

`kubit lab apps` runs the daemon in the foreground for those repos and opens the console,
straight on the cluster's page when exactly one cluster repo is given;
`kubit serve lab apps` does the same without a browser. A dir with `cluster.yaml` is a
cluster repo: Kubit decrypts it with your age key, adopts the cluster when its nodes
answer (again whenever discovery sees one of its machines), and watches the files
(fsnotify) to reload on change. A dir without one only feeds the secrets
editor. It binds `127.0.0.1:8090`; a non-loopback bind requires a bearer token
(`--token` / `KUBIT_TOKEN`).

The console writes only to the served repos and never commits:

- **Add** on one or more maintenance machines (Discovery, machine page) either creates a cluster
  repo in a directory you type (`cluster.yaml` designed from the machines' inventory,
  `secrets.sops.yaml` and `.sops.yaml` for your age key, created at
  `~/.config/sops/age/keys.txt` when missing; an existing `cluster.yaml` is never
  overwritten) and serves it, or appends the machines to a served cluster's `spec.nodes`
  with the next free hostnames. Both go through a review of the exact result first
  (*Discovery*, above).
- **Remove** on a node's page deletes the node from `spec.nodes`.
- **Enable / Disable** on an Add-ons card sets `spec.platform.<add-on>.enabled` (MetalLB asks
  for its range, Flux for its repository); Storage offers it for Longhorn when no
  StorageClass exists.
- **Generate / Replace / Rescan** on the Flux card writes `flux.deployKey` and
  `flux.knownHosts` to `secrets.sops.yaml` (stale-hash checked, re-encrypted for the file's
  own recipients).
- **Upgrade** on an update notice (Home, Overview) sets `talosVersion` and
  `kubernetesVersion` to the latest supported pair and opens Changes.
- Clicking a node's address on the Nodes tab sets its `network` (static or DHCP, address, gateway,
  DNS), prefilled from the node's live config, and moves `controlPlane.endpoint` along when
  it points at that node. The Add review step chooses DHCP or a static address per machine,
  with gateway and DNS prefilled from the machines' leases; an address that already answers
  blocks the write.
- **Repository › cluster.yaml** is an editor (YAML highlighting, ⌘S saves). A save is
  validated like `kubit plan` validates the file, keeps `metadata.name`, is refused when the
  file changed on disk since it was opened, and is written atomically.
- **Changes** shows the cluster's current plan. Kubit plans by itself, read-only (Talos dry
  runs, `tofu plan -lock=false`), when cluster.yaml or secrets.sops.yaml changes, after an
  apply, when the cluster's state changes (connecting → declared → ready), when a node's
  reachability, readiness or versions change, and every 30 minutes while a console is open.
  Plans for one cluster run one at a time; an edit cancels a plan that is still running. No
  plan runs while an apply holds the cluster, from the console or the CLI (`kubit-apply`
  Lease), and the planner plans again when that Lease is released or expires (a watch on
  it, not a retry).
- **Apply** runs only the plan you reviewed: the console sends the plan's hash (a digest of
  cluster.yaml, secrets.sops.yaml and the planned changes). Kubit answers at once when the
  hash matches the latest plan and runs the rest in the background: `lock` takes the Lease,
  `review` plans again under it and stops with "the plan changed since you reviewed it" when
  the hash differs. Removals need *allow removal*; the log streams live and survives a page
  reload.

Every cluster page header, the sidebar and Home show the plan state: *in sync*, *N
changes*, *planning*, *applying*, *N problems* or *plan failed*. One-time housekeeping
(moving add-on state from an older repo into the cluster) is listed apart and does not
count as a change.

Node, add-on and version edits go through the YAML tree, so comments and order survive, and
are validated like `kubit plan` validates the file before it is written. Every console edit
carries the version (hash) of cluster.yaml it was made from (the cluster row pushed to the
console carries it) and is refused when the file changed since; writes to one file are
serialised and atomic (temporary file, fsync, rename), including `.sops.yaml`,
kustomization.yaml, secrets and `kubit init`. Kubit's own write and your editor's both reload
through the file watcher. A reload publishes the declared spec at once, merged with the last
observed state, and observes the nodes after; a console write answers with the new file hash
and its button stays busy until the cluster row with that hash arrives. Discovery's scan, the Secrets tab, *Take snapshot* on Backups (an etcd snapshot into the
repo's `snapshots/`) and *Stop Kubit* are the other writes.

A served repo's cluster is listed at once as `connecting` until Kubit has reached its
nodes: then it is `ready` (or `bootstrapped`) when a node is a member, `declared` when its
machines are in maintenance mode, and stays `connecting` until discovery sees one of its
machines. A `declared` or `connecting` cluster's page shows only Changes,
Secrets and Repository; *Apply* creates a declared one.

The Nodes tab shows each node's model, CPUs, memory and largest disk (read once per daemon run
over the Talos API for members, since machine records live in memory) and its CPU package temperature (`coretemp`, `k10temp` or the SoC
sensor) and, when it has one, its NVMe temperature, read from `/sys/class/hwmon` over
the Talos API on every watcher tick and coloured by the chip's own high and critical
limits.

Clicking a controller on Workloads lists the pods its selector matches; a pod opens its logs
and events, also from a machine's Kubernetes tab. Network lists Services, Ingresses and Gateway
API HTTPRoutes (with whether a Gateway accepted them), all live. ⌘K jumps to any page and runs
*Plan*, *Check now* and *Scan the network*. Actions answer at once and show their progress in
the page (*Scanning*, *Taking snapshot*, the plan pill, the apply log); none blocks on the
work.

| route | shows |
|---|---|
| `/` | open alerts, notices, clusters, machine counts |
| `/clusters/<name>/…` | Overview, Changes, Nodes, Workloads, Network, Storage, Add-ons, Secrets, Backups, Repository (cluster.yaml editor, Git, Certificates; kubeconfig) |
| `/machines/<mac>` | Overview, Hardware, Kubernetes, Services, Logs (`?tab=`) |
| `/discovery` | machines to add, machines waiting for apply, other machines, scan, PXE state |
| `/secrets` | the SOPS files of every served repo, each linked to its cluster's Secrets tab |

Everything is live over one WebSocket (`/api/v1/ws`): state changes, watcher status,
health events and `refresh {cluster, scope}` from Kubernetes informers and the repo
watcher; reconnects replay from `?since=`. Nothing polls; the only UI timer is
`web/src/clock.ts`.

Endpoints (all `GET` unless noted): `clusters`, `clusters/{n}/status|yaml|kubeconfig|
config|image|addons|flux|builds|sops|certificates|snapshots[/{id}]|events|samples|
workloads|pods|namespaces|network|storage`, `nodes`,
`nodes/{ip}/inventory|services|logs|kubernetes`, `versions`, `pxe`, `ws`; `GET discover` (subnets, last scan), `POST discover` (scans `targets`, or the default subnets, in the background; machines arrive as `machine` messages),
`POST clusters/{n}/check` (the watcher observes the cluster now),
`POST daemon/stop`, `POST design` (`{cluster | dir, name, vip, machines: [{mac, role}]}`; the
result without writing), `POST design/checks` (the same with the VIP and address probes), `POST repos` (same body, writes a new repo), `POST clusters/{n}/nodes`
(`{machines: [{mac, role}], hash}`), `clusters/{n}/repo` (directory and git state), `DELETE clusters/{n}/nodes/{hostname}?hash=`,
`GET|PUT clusters/{n}/nodes/{hostname}/network` (`{static, address, gateway, nameservers, hash}`),
`PUT clusters/{n}/platform/{add-on}` (`{enabled, range, repository, hash}`), `PUT clusters/{n}/versions`
(`{talosVersion, kubernetesVersion, hash}`), `POST clusters/{n}/snapshots` (takes an etcd snapshot),
`PUT clusters/{n}/yaml` (`{yaml, hash}`; this and the other cluster.yaml writes answer the new `{hash}`), `plans` (every cluster's plan state), `GET clusters/{n}/plan`
(`{summary, plan}`), `POST clusters/{n}/plan` (plan again), `GET|POST clusters/{n}/apply`
(`{allowRemoval, planHash}`, 202; lines arrive as `apply` messages, plan states as `plan` messages),
`GET secrets` (`{repos, labels}`), `GET clusters/{n}/secrets` (Flux source, repos, files),
`GET secrets/values?repo&file` (one file decrypted, with its hash), `PATCH secrets/file`
(`{repo, file, hash, set: [{path, value}], remove: [path]}`), `DELETE secrets/file?repo&file&hash`,
`POST secrets/move` (`{repo, file, to, hash}`), `POST secrets/files` (`{repo, file, name, namespace,
type, stringData}`), `POST clusters/{n}/sops/flux[?repo=i]`, `GET|POST clusters/{n}/flux/key`
(`{hash, hostsOnly}`; public key, fingerprint, host keys). Unknown `/api/`
paths answer 404 JSON.

**Secrets.** A cluster's *Secrets* tab lists the Kubernetes Secrets Flux applies to it: the
`*.sops.yaml` files under `platform.flux.repository.path` in every served repo whose git remote
is `platform.flux.repository.url` (https, ssh and scp forms match), plus any SOPS files in the
cluster repo itself. Each row shows namespace, type, key count, whether Flux can decrypt it
(the cluster's Flux key is a recipient), whether Flux applies it at all (every
`kustomization.yaml` from the Flux path down lists it; with none, Flux generates one and takes
every file), its git state and age. When Flux syncs a repo that is not served, the tab says
which checkout to serve. The global *Secrets* page lists the SOPS files of every served repo
and opens each in its cluster's tab.

Files are read without decrypting; *Show values* decrypts one file. *Edit* changes any number
of keys (add, rename, change, remove) and shows a review of the key names before the write;
`data` values are shown decoded and written base64, binary values are kept as they are. A
save re-encrypts for the file's own recipients and rules, is atomic, and is refused when the
file changed on disk since it was read. *New Secret* takes an Opaque, TLS (`tls.crt`,
`tls.key`, loadable from files) or registry (`.dockerconfigjson` from server, user and token)
template, encrypts only `data`/`stringData` for the recipients of the new path's `.sops.yaml`
rule, and adds the file to its folder's `kustomization.yaml` when there is one. *Rename*
re-encrypts when the new path's rule names other recipients and moves the kustomization entry;
*Delete* removes both. Names, namespaces and keys are checked like Kubernetes checks them, and
TLS and registry secrets need their keys. Kubit never commits. Encrypted comments are dropped
on a write.

`.sops.yaml` holds two rules: `(^|/)secrets\.sops\.yaml$` for your keys only (the Talos
bundle, the Flux key, the state passphrase), and `\.sops\.ya?ml$` for your keys plus the
cluster's Flux recipient, so Flux can decrypt app secrets. The `sops` CLI picks the same rule
for relative and absolute paths. Repos made before this show a notice on the cluster's
Secrets tab; *Let Flux decrypt* adds the Flux key to that repo's `.sops.yaml` (splitting a
shared rule) and re-encrypts the app secrets Flux can't read.

**Settings** come from `kubit serve --config kubit.yaml` (defaults otherwise):

```yaml
factoryUrl: https://factory.talos.dev
pxeStatusUrl: http://127.0.0.1:8069/status.json
alerts:
  minSeverity: warn
  webhookUrl: https://hooks.slack.com/…
discoverySubnets: [192.168.1.0/24]
```

## PXE

`sudo kubit pxe [repos…] [--iface en0] [--talos-version …] [--schematic …]` is a
proxyDHCP (the LAN's DHCP keeps assigning addresses) with TFTP for iPXE and an HTTP iPXE
script on :8069 that boots the Talos kernel and initramfs into maintenance mode. Assets
come from the Image Factory through `~/.kubit/cache`. A MAC declared in any of the
repos' cluster.yaml boots its own disk; every other MAC gets Talos (`--closed`: none).
`--http-only` serves only the script and assets. Needs root and the machines' L2
segment. While it runs it keeps `~/.kubit/run/pxe`; `kubit serve` watches that directory and
follows `status.json?watch` (server-sent events) for as long as the PXE process lives, so the
Discovery page shows boots as they happen without polling.

## Health watcher

`kubit serve` observes every ready cluster (15 s, `--watch-interval`) and keeps samples in
memory: every tick for 30 minutes and one per minute for a day. The Overview's capacity
trend shows 5 minutes by default, and 30 minutes, 1, 6 or 24 hours. Alerts are conditions: `talos.unreachable`, `node.notready`,
`api.unreachable`, `etcd.unhealthy`, `cert.expiring` and `observer.offline` stay open
while they hold and resolve themselves; there is nothing to acknowledge. A fault must
hold for three ticks without a gap; a laptop that slept re-baselines instead of
alerting. When the gateway is unreachable too, the observer is offline and cluster
alerts pause. Alerts stay quiet while a `kubit apply` holds the cluster's
`kube-system/kubit-apply` Lease and for 10 minutes after. Changes (cordons, version
steps, removed nodes, etcd membership and leader) are logged as events.

The webhook gets each raise at or above `alerts.minSeverity` and its resolution.
Faults present when the daemon starts open without a webhook. Alerts, events and samples
live only in the daemon's memory.

## Backups

Off until `spec.backup` is declared. Then `kubit apply` gives every control plane Talos
API access for the `os:etcd:backup` role from namespace `talos-backup` (a config change,
no reboot) and installs [talos-backup](https://github.com/siderolabs/talos-backup)
(`ghcr.io/siderolabs/talos-backup:v0.1.0-beta.2`): a Talos `ServiceAccount`, a Secret with
the S3 credentials and a CronJob on `schedule` that streams an etcd snapshot, encrypts it
for `ageRecipients` and uploads it to `s3://bucket/prefix` (prefix defaults to the cluster
name; `compression` adds zstd). The S3 credentials live in the repo's secrets, never in
cluster.yaml: add `backup.accessKeyID` and `backup.secretAccessKey` with
`sops lab/secrets.sops.yaml`; `kubit plan` refuses a declared backup without them.
Kubit no longer takes scheduled snapshots itself. Not yet run against a bucket.

For disaster recovery, `kubit etcd snapshot|list|restore <dir>` stays: a
snapshot is verified (bbolt, key count), gzipped and age-encrypted to the `.sops.yaml`
recipients into the repo's `snapshots/` (git-ignored); restore wipes EPHEMERAL on every
control plane and rebuilds etcd from it (`--yes` required). A talos-backup snapshot restores the same way
once decrypted with `age -d` (`talosctl bootstrap --recover-from`).

## Running Kubit

Kubit runs only when you start it. `kubit` (or `kubit serve`) binds its port, so a
second daemon on the same port refuses to start; Ctrl-C or *Stop Kubit* stops it
cleanly. Clusters never depend on Kubit: while it is
stopped, alerts and discovery pause. Start it from a terminal that
stays open: on macOS a daemon started by an app that has quit loses local-network
access. Under `sudo` (`kubit pxe`) Kubit uses the invoking user's `~/.kubit`.

Deleting `~/.kubit` loses nothing but downloads.

**Commands:** `kubit [dirs…]` (daemon and console), `serve`, `init`, `plan`, `apply`, `deploy-key`,
`talosconfig`, `kubeconfig`, `pxe`, `etcd snapshot|list|restore`, `version`. Everything else is talosctl or
kubectl with the derived credentials.

## Status

Built and verified before the IaC turn (tag `click-ops` keeps the removed code):
Talos create/add/remove/upgrade on VMs and an HP EliteDesk, platform add-ons via
OpenTofu, Flux + SOPS app delivery, export, health watcher and alerts, etcd snapshot and
restore drill, live console, PXE (AMT-forced UEFI boot verified). Traefik and the Gateway
API CRDs are unit-tested only.

Roadmap (2026-10-05):

- [x] 0 — housekeeping: `click-ops` tag, credentials out of the repo
- [x] 1 — cut: lab hosts, AMT/Redfish, inventory and create wizard, node-edit forms,
  accounts/OIDC, off-site and heartbeat, settings pages; console read-only; Discovery
  page; `serve --config`
- [x] 2 — repo as source of truth: `kubit init`, SOPS-compatible `secrets.sops.yaml`,
  derived talosconfig/kubeconfig, PXE decides from the repos; state in the repo lands with apply
- [x] 3 — `kubit plan` / `kubit apply` converge (create, add, config, upgrades, guarded
  removal, platform), Lease lock, exit codes, encrypted tofu state in the repo;
  `cluster create|apply`, `node add|remove`, `upgrade`, `platform`, `sops` commands
  removed. Unit-tested; not yet run against a cluster
- [x] 4 — `kubit serve <dirs…>` (adopt, watch, reload), discovery scans served subnets every minute, console secrets editor (verified against
  the sops CLI and live external edits)
- [x] 5 — `spec.backup` → talos-backup add-on, off by default; the daemon's snapshot
  schedule, retention and `backup.stale` are gone
- [x] 6 — migration: `kubit export <cluster> --repo <dir>` (spec, secrets, Flux key,
  encrypted state); repo clusters' secrets held in memory and dropped from SQLite;
  `etcd` takes a repo dir
- [x] Cleanup — CLI down to the commands above; operations/Activity, audit log,
  maintenance windows, the daemon's PXE decision and AMT/lab machine kinds removed;
  apply quiets alerts through its Lease; dead tables and columns dropped
- [x] 7 — no database: SQLite, the master key and `kubit export` removed; snapshots are
  repo files; create resumes from live state; host firewall, disk encryption, pools,
  watchdog, SMTP, workload alerts, alert acks and pushed stages cut
- [x] 8 — console edits the repo: new cluster repo from a discovered machine, node add
  and remove in `cluster.yaml`, plan and apply with a live log; `declared` state for a
  repo whose cluster does not exist yet. Unit-tested against a scratch repo
- [x] 9 — discovery to cluster: multi-select, review before writing, free-VIP check,
  MAC-verified installs, disks listed in plan and confirmation, repository panel with
  live git state, no idle sleep during apply. Browser-tested with five simulated
  machines; create not yet run on hardware
- [x] 10 — console as an IaC editor (2026-10-06): an always-current plan in every header
  (apply only what was reviewed, by hash); Changes and Repository tabs with a cluster.yaml
  editor; addresses for every node (static, move, release; control planes with the endpoint
  and etcd peer URL), unit-tested, not yet run on hardware; per-cluster, Flux-aware Secrets
  with review before save and Opaque/TLS/registry templates; add-on toggles, *Upgrade*,
  *Take snapshot*; member hardware; HTTPRoutes; view clean-up. Browser-tested on `lab`
  read-only and on scratch repos

Next: run the roadmap against `lab` (export with `dbeba6f`, plan with no changes, an
upgrade, a node add and removal, a talos-backup run) before merging `iac` into `main`.
