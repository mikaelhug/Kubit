# Operations

How Kubit finds machines, plans and applies changes, runs the add-ons, network-boots
machines, watches cluster health and backs up etcd.

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
7. **Settings › Git** shows the branch, uncommitted files and unpushed commits, live
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
restored from the `pre-move` snapshot ([Backups](#backups)).

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

**Destroy.** `kubit destroy lab` (or *Settings › Danger zone › Destroy*, after typing the cluster name; the log runs on Changes)
takes the apply Lease, resets the workers and then the control planes without draining or
leaving etcd (Talos reset of STATE and EPHEMERAL, reboot; the installed Talos stays), and
waits up to 10 minutes until every node answers in maintenance mode, at its address or by
MAC in its /24. Nodes come back on DHCP. The repo is kept: `kubit apply` creates the cluster
again from it; to start over through Discovery, move the repo away. `--yes` skips the prompt.

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
- A repo from an older Kubit with `state/platform.tfstate` gets a one-time step: *Apply*
  moves that state into the cluster and the log asks you to commit the file's removal.
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
  so turning Traefik off never deletes Gateways or routes.
- **Longhorn** runs only on data disks or `data-system` (labelled at machine-config
  time), reserve 5 %, replicas = min(3, storage nodes). Enabling it adds the
  iscsi/util-linux extensions; `kubit apply` re-images the nodes before the platform.
- **Builds**: in-cluster `registry:3` + rootful BuildKit in `kubit-builds`; nodes pull
  `registry.kubit/<app>` through a fixed ClusterIP mirror. Needs Longhorn.
- **Flux**, headless (source, kustomize, helm, notification). One Flux Kustomization
  per app keeps a broken app from blocking the rest.

`platform.<addon>.values` is passed to Helm only when set.

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
`destroy`, `talosconfig`, `kubeconfig`, `pxe`, `etcd snapshot|list|restore`, `version`. Everything else is talosctl or
kubectl with the derived credentials.
