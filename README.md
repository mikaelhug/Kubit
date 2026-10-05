# Kubit

Declarative provisioner for Talos Linux clusters on bare metal and VMs. One Go binary:
CLI, a foreground daemon (health, alerts, discovery) and a read-only web console.

Since 2026-10-05 Kubit is being reshaped from a click-ops console into an IaC tool: the
cluster's desired state lives in a git repo you choose, `kubit plan` / `kubit apply`
converge it, and the console only observes. The roadmap is under *Status*.

## Architecture

| Layer | Owner | Mechanism |
|---|---|---|
| Discovery, machine config, apply, bootstrap, kubeconfig, OS/K8s upgrades, drain, reset, etcd | Kubit | Talos API via `siderolabs/talos/pkg/machinery` + `client-go` |
| Platform add-ons: MetalLB, Traefik, gVisor, metrics-server, cert-manager, Longhorn, Flux, Builds | OpenTofu (`infra/platform/`), run by Kubit with a pinned binary | `hashicorp/helm`, `alekc/kubectl` |
| User workloads | Flux (headless), syncing an apps repository you own | GitRepository + Kustomization `flux-system/flux-system` from `platform.flux.repository` |
| App secrets | SOPS files in the apps repository, one age key per cluster | kustomize-controller decrypts in the cluster |
| Escape hatch | You | `kubit cluster export`: `infra/talos/` HCL + native artefacts, never run by Kubit |

A cluster lives in a repo directory you choose (*Cluster repo*). `~/.kubit` (SQLite,
AES-GCM sealed with a master key in the macOS Keychain) holds caches and daemon data.

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

## Cluster repo

```
lab/
  cluster.yaml         the declaration; Kubit never writes it after init
  secrets.sops.yaml    Talos secrets bundle, Flux age key, platform state passphrase
  .sops.yaml           age recipients for *.sops.yaml
  state/               OpenTofu state, encrypted by tofu
  .gitignore           talosconfig, kubeconfig, .terraform/
```

`kubit init lab --nodes 192.168.1.0/24` probes the machines in maintenance mode,
proposes `cluster.yaml` for them (`config.Design`: bare-metal control planes first, VIP
and MetalLB range in their /24, firewall and `nodeID` encryption on) and writes the
secrets SOPS-encrypted for the recipients in `.sops.yaml`, `--age`, or your own age key
(created at the sops default path when you have none; back it up). It never overwrites.

Kubit reads and writes SOPS files itself (`internal/sops`: AES-256-GCM values, age-wrapped
data key, MAC), compatible with the `sops` CLI both ways, so `sops lab/secrets.sops.yaml`
edits the same file. Keys come from `SOPS_AGE_KEY`, `SOPS_AGE_KEY_FILE` or the sops
default `keys.txt`.

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
    firewall: true                 # host ingress firewall; absent = off
  storage:
    systemDisk: true               # rest of the install disk → data-system for Longhorn
    ephemeralSize: 40GiB
    encryption: nodeID             # nodeID | tpm; absent = off; fixed after install
  patches:                         # Talos strategic-merge patches, every node
    - machine: { sysctls: { vm.max_map_count: "262144" } }
  pools:                           # default: controlplane + worker
    - { name: controlplane, role: controlplane }
    - { name: worker, role: worker }
    - name: gpu
      role: worker
      labels: { workload: gpu }
      taints: { nvidia.com/gpu: "true:NoSchedule" }
      extensions: [siderolabs/nvidia-open-gpu-kernel-modules-lts]
      installDisk: { selector: { minSize: 100GB, type: nvme } }
  nodes:
    - hostname: cp-01
      ip: 192.168.64.2
      mac: "52:54:00:4b:49:01"
      pool: controlplane
      arch: arm64
      kvm: true
      tpm: true
      watchdog: true
      installDisk: { path: /dev/vda }
      dataDisks: [/dev/vdb]        # whole disks → xfs at /var/mnt/data-N
    - hostname: gpu-01
      ip: 192.168.64.150
      pool: gpu
      network: { addresses: [192.168.64.150/24], gateway: 192.168.64.1, vlan: 0 }
  backup:
    etcd: { interval: 6h, keep: 28 }
  maintenance: { window: "Sat,Sun 02:00-06:00", timezone: Europe/Stockholm }
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
      repository: { url: https://github.com/you/apps.git, branch: main, path: ./flux }
```

The Discovery page copies a node entry for any machine in maintenance mode.

**Generated configs** are Talos 1.14 multi-document: v1alpha1 core,
`UnattendedInstallConfig` (per-pool installer + disk selector), `HostnameConfig`,
`KubeNodeConfig` (pool and node labels, taints), `LinkAliasConfig` `uplink` by MAC,
`DHCPv4Config` or static `LinkConfig` + `RouteConfig` (+ `VLANConfig`),
`ResolverConfig`, `TimeSyncConfig`, `Layer2VIPConfig`, Flannel with
kube-network-policies, `DiscoveryServiceConfig`, firewall rules, LUKS2 volume configs,
`WatchdogTimerConfig` and, with `spec.auth.oidc`, a JWT authenticator for kubectl SSO.
Patches apply last (cluster → pool → node) and every save validates the generated
configs with Talos's metal-mode rules. `config.Lint` reports advisory findings.
Talos < 1.14 is rejected.

**Disks.** `installDisk` holds Talos; each `dataDisks` entry becomes a whole-disk xfs
`UserVolumeConfig` at `/var/mnt/data-N`. With `storage.systemDisk` a node without data
disks keeps `ephemeralSize` for `/var` and gives the rest of the install disk to
`data-system` (fresh installs only; Longhorn must be on). `storage.encryption: nodeID`
derives the key from the machine UUID; `tpm` needs Secure Boot images Kubit does not
install yet.

### Host firewall

With `network.firewall: true` every node blocks inbound traffic except:

| Rule | Proto | Ports | From | Nodes |
|---|---|---|---|---|
| `apid` | tcp | 50000 | anywhere | all |
| `kubelet` | tcp | 10250 | cluster subnets + pod CIDR | all |
| `flannel-vxlan` | udp | 4789 | cluster subnets | all |
| `kubernetes-api` | tcp | 6443 | anywhere | control planes |
| `trustd` | tcp | 50001 | cluster subnets | control planes |
| `etcd` | tcp | 2379-2380, 2383 | cluster subnets | control planes |
| `metallb-tcp/udp` | tcp/udp | 7946 | cluster subnets | with MetalLB |
| `ingress-nodeports` | tcp | 30000-32767 | anywhere | ingress without MetalLB |

Cluster subnets are each node's static prefixes, else the /24 of its IP.

## Secrets

`~/.kubit/kubit.db` (SQLite, WAL). Secrets bundle, talosconfig, kubeconfig and node
configs are AES-256-GCM sealed with a 32-byte master key from the macOS Keychain
(`kubit` / `master-key`), `KUBIT_MASTER_KEY` (base64), or a `master.key` file (0600) in
`$KUBIT_HOME` when no keyring is reachable. `kubit key export` prints it.

**App secrets (SOPS + age).** One age identity per cluster, generated on first use and
sealed in `sops_keys`; it outlives the cluster, so a rebuild under the same name
decrypts again. Every platform apply with Flux installs it as `flux-system/sops-age`,
and the root Kustomization decrypts with it. Encrypt files to your own key plus each
cluster's recipient (`kubit sops recipient <cluster>`). `kubit sops export|import` move
the key.

## Discovery

`kubit discover <cidr|ip>…` (or Discovery → Scan) probes :50000 and reads the insecure
maintenance API: MAC of the uplink, arch, CPUs, RAM, disks, KVM, TPM, watchdog, SMBIOS
UUID and serial. Machines are keyed by MAC, so a new DHCP lease keeps the row and raises
`machine.ip-changed` for members. Kinds are derived: **member** (in a cluster),
**maintenance**, **configured** (Talos with a config Kubit did not apply), **booting**,
**unbooted**. The daemon reprobes maintenance and configured machines in the background
and pushes changes to the console.

## Cluster lifecycle

`kubit cluster create -f cluster.yaml`: preflight → schematic → secrets and node configs
→ apply in parallel → wait for the install reboot → bootstrap etcd → kubeconfig → nodes
Ready → platform apply. Re-running it resumes. `cluster apply` regenerates and applies
every node config, control planes first, then syncs the bootstrap manifests and the
platform. `node add`, `node remove` (drain → delete → reset; refuses to drop below one
control plane, or to two without `--force`), `upgrade talos|kubernetes`, `status`,
`cluster export`.

**Upgrades** run per node, control planes first: pull the installer, stage it,
check etcd health (never with exactly two members), drain (5 min, PDBs respected),
reboot, wait for Ready and the etcd member count, uncordon. A changed extension set
re-images even at the same version. Every upgrade starts with prechecks (API, etcd,
nodes Ready, free `/var`, published target, supported version pair, deprecated API use)
and a `pre-upgrade` etcd snapshot.

**Config freshness.** Kubit compares each node's applied config with what the
declaration generates now; `GET /clusters/{name}/config` lists nodes that are behind.

## Platform layer (OpenTofu)

`~/.kubit/clusters/<name>/infra/platform/` is rendered from embedded templates plus
`terraform.tfvars.json`, then `tofu init/plan/apply` with a pinned, checksum-verified
binary. Charts are pinned in `internal/tofu/render.go`. Notes:

- `metallb-system` and `longhorn-system` are `privileged`; MetalLB is layer-2 only (FRR
  off); control planes keep LoadBalancer announcements when they run workloads.
- Every add-on has resource requests; releases are `atomic`.
- **Traefik** replaces ingress-nginx: IngressClass `traefik` (default) plus a
  compatibility class `nginx`, Gateway API CRDs v1.6.1 shipped with Kubit. The old
  ingress address is pinned during the migration.
- **Longhorn** runs only on data disks or `data-system` (labelled at machine-config
  time), reserve 5 %, replicas = min(3, storage nodes). Enabling it needs the
  iscsi/util-linux extensions: upgrade Talos first.
- **Builds**: in-cluster `registry:3` + rootful BuildKit in `kubit-builds`; nodes pull
  `registry.kubit/<app>` through a fixed ClusterIP mirror. Needs Longhorn.
- **Flux**, headless (source, kustomize, helm, notification). One Flux Kustomization
  per app keeps a broken app from blocking the rest. `flux.not-ready` alerts.

`platform.<addon>.values` is passed to Helm only when set.

## Export

`kubit cluster export <name> -o dir` writes `secrets.yaml`, talosconfig, machine
configs, kubeconfig and `infra/talos/` for the `siderolabs/talos` provider (`tofu apply`
is a no-op on a live cluster). A re-export removes only files it wrote.

## Console and API

`kubit` runs the daemon in the foreground and opens the console; `kubit serve` does the
same without a browser. It binds `127.0.0.1:8090`; a non-loopback bind requires a bearer
token (`--token` / `KUBIT_TOKEN`).

The console is read-only apart from Discovery's scan, alert acknowledgement and *Stop
Kubit*:

| route | shows |
|---|---|
| `/` | open alerts, notices, clusters, machine counts, recent activity |
| `/clusters/<name>/…` | Overview, Nodes, Workloads, Network, Storage, Add-ons, Backups, Config (cluster.yaml, kubeconfig, certificates) |
| `/machines/<mac>` | Overview, Hardware, Kubernetes, Services, Logs |
| `/discovery` | machines in maintenance mode with *Copy node entry*, scan, PXE state |
| `/operations` | operations and audit log |

Everything is live over one WebSocket (`/api/v1/ws`): store changes, watcher status,
health events, operation events and `refresh {cluster, scope}` from Kubernetes
informers; reconnects replay from `?since=`. Nothing polls; the only UI timer is
`web/src/clock.ts`.

Endpoints (all `GET` unless noted): `clusters`, `clusters/{n}[/status|yaml|kubeconfig|
config|image|addons|flux|builds|sops|certificates|maintenance|snapshots[/{id}]|events|
samples|service-health|workloads|pods|namespaces|network|storage]`, `nodes`,
`nodes/{ip}/inventory|services|logs|kubernetes`, `machines[/{mac}]`, `operations[/{id}]`,
`audit`, `observer`, `versions`, `pxe`, `pxe/decide`, `version`, `ws`; `POST discover`,
`POST events/{id}/ack`, `POST clusters/{n}/events/ack`, `POST daemon/stop`. Unknown
`/api/` paths answer 404 JSON.

**Settings** come from `kubit serve --config kubit.yaml` (defaults otherwise):

```yaml
factoryUrl: https://factory.talos.dev
pxeStatusUrl: http://127.0.0.1:8069/status.json
pxeEnrollment: open          # closed: unknown MACs get no Talos
alerts:
  minSeverity: warn
  webhookUrl: https://hooks.slack.com/…
  smtp: { host: smtp.example.com, port: 587, from: kubit@example.com, to: [ops@example.com], username: kubit, tls: starttls }
  ignoreNamespaces: [dev]
```

The SMTP password comes from `KUBIT_SMTP_PASSWORD`, never the file.

## PXE

`sudo kubit pxe --iface en0 [--talos-version …] [--schematic …]` is a proxyDHCP (the
LAN's DHCP keeps assigning addresses) with TFTP for iPXE and an HTTP iPXE script on
:8069 that boots the Talos kernel and initramfs into maintenance mode. Assets come from
the Image Factory through `~/.kubit/cache`. With `--repo lab` (repeatable) a MAC declared
in any cluster.yaml boots its own disk and every other MAC gets Talos (`--closed`: none);
without it the daemon decides.
`--http-only` serves only the script and assets. Needs root and the machines' L2
segment.

## Health watcher

`kubit serve` polls every ready cluster (15 s, `--watch-interval`), stores samples (24 h
full, 30 d hourly) and raises events: `talos.unreachable`, `node.notready`,
`api.unreachable`, `etcd.unhealthy`, `etcd.members`, `lb.lost`, `node.memory-small`,
`cert.expiring`, `backup.stale`, with recoveries that resolve them. Reachability alerts
need three consecutive ticks without a gap; a laptop that slept re-baselines instead of
alerting. When the gateway is unreachable too, the observer is offline and cluster
alerts pause. Every 60 s it also derives workload alerts (`workload.unavailable`,
`pod.crashloop`, `pvc.pending`, `service.no-endpoints`, `ingress.no-address`,
`lb.pool-exhausted`, `flux.not-ready`), raised after two bad and cleared after three
good collections. Alerts at or above `alerts.minSeverity` go to the webhook and SMTP.

## etcd snapshots

`kubit etcd snapshot|list|download|restore <cluster>`. A snapshot is verified (bbolt,
key count, sha256), gzipped and sealed under `~/.kubit/clusters/<name>/snapshots/`.
The daemon takes one every `backup.etcd.interval` while the cluster is healthy and idle
and prunes scheduled ones to `keep`. Restore wipes EPHEMERAL on every control plane and
rebuilds etcd from the snapshot (`--yes` required). Phase 5 replaces scheduled snapshots
with an opt-in talos-backup add-on.

## Running Kubit

Kubit runs only when you start it. `kubit` (or `kubit serve`) holds `serve.lock` in
`KUBIT_HOME`, so a second daemon refuses to start; Ctrl-C or *Stop Kubit* stops it
cleanly (refused while operations run). Clusters never depend on Kubit: while it is
stopped, alerts, discovery and scheduled snapshots pause. Start it from a terminal that
stays open: on macOS a daemon started by an app that has quit loses local-network
access. Under `sudo` (`kubit pxe`) Kubit uses the invoking user's `~/.kubit`.

`kubit backup -o file.kubitbak` writes a sealed tar.gz of `~/.kubit`; `kubit restore`
unpacks it into an empty home and needs the same master key.

## Status

Built and verified before the IaC turn (tag `click-ops` keeps the removed code):
Talos create/add/remove/upgrade on VMs and an HP EliteDesk, platform add-ons via
OpenTofu, Flux + SOPS app delivery, export, health watcher and alerts, etcd snapshot and
restore drill, live console, PXE (AMT-forced UEFI boot verified). Traefik, the Gateway
API CRDs, host firewall, disk encryption and pushed Talos stages are unit-tested only.

Roadmap (2026-10-05):

- [x] 0 — housekeeping: `click-ops` tag, credentials out of the repo
- [x] 1 — cut: lab hosts, AMT/Redfish, inventory and create wizard, node-edit forms,
  accounts/OIDC, off-site and heartbeat, settings pages; console read-only; Discovery
  page; `serve --config`
- [x] 2 — repo as source of truth: `kubit init`, SOPS-compatible `secrets.sops.yaml`,
  derived talosconfig/kubeconfig, `kubit pxe --repo`; state in the repo lands with apply
- [ ] 3 — `kubit plan` / `kubit apply` converge
- [ ] 4 — `kubit serve <dirs…>`, status, discovery polish, secrets editor
- [ ] 5 — talos-backup add-on, off by default
- [ ] 6 — migration: `kubit export --repo`
