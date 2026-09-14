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
  network: { podCIDR: 10.244.0.0/16, serviceCIDR: 10.96.0.0/12 }
  nodes:
    - hostname: cp-01
      ip: 192.168.64.2
      mac: "52:54:00:4b:49:01"     # optional; selects the VIP uplink on multi-NIC hosts
      role: controlplane           # controlplane | worker
      arch: arm64                  # amd64 | arm64
      kvm: true                    # /dev/kvm present → also labelled for runsc-kvm
      installDisk: { path: /dev/vda }                              # or:
      # installDisk: { selector: { minSize: 100GB, type: nvme, model: "Samsung*" } }
  platform:
    metallb: { enabled: true, range: 192.168.64.200-192.168.64.220 }
    ingressNginx: { enabled: true }
    gvisor: { enabled: true }
    metricsServer: { enabled: true }
    certManager: { enabled: false }
    argocd: { enabled: false }
```

Generated machine configs are Talos 1.14 multi-document: the v1alpha1 core plus
`UnattendedInstallConfig` (installer image + CEL disk selector), `SysctlConfig`
(`user.max_user_namespaces=11255` for gVisor), `HostnameConfig`, `KubeNodeConfig`
(labels `sandbox.runtime/gvisor[-kvm]`, NoSchedule taint when control planes are
dedicated) and, on control planes with a VIP, `LinkAliasConfig` + `Layer2VIPConfig`.

## Secrets

`~/.kubit/kubit.db` (SQLite, WAL). Secret columns (secrets bundle, talosconfig,
kubeconfig, per-node machine config) are AES-256-GCM sealed with a 32-byte master key
kept in the macOS Keychain as service `kubit` / account `master-key`.
`KUBIT_MASTER_KEY` (base64) overrides the keyring.

## Discovery

`kubit discover 192.168.64.0/24` TCP-probes :50000, then tries the insecure maintenance
API. Nodes that answer are `maintenance` (inventory recorded: MAC of the link holding the
IP, arch, CPUs, RAM, disks, `/dev/kvm` presence); nodes that reject the insecure TLS
handshake are `configured`. Results are upserted into the store, keyed by IP; a rescan
never clears cluster membership or previously captured hardware.

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
wrong, so restarts do not replay history. `GET /clusters/{name}/status` serves the
watcher's latest result; `?fresh=true` forces a live query.

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
- [ ] M4 — workloads / network / storage views
- [ ] M5 — add-on status and values, ArgoCD/cert-manager verified, cluster settings form
- [ ] M6 — fleet: inventory detail, PXE page, Kubit settings, backup/restore
- [ ] M7 — tests, CI, packaging, docs
