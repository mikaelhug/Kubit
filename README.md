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
hack/talosver/    throwaway maintenance-mode probe (removed once `kubit discover` exists)
```

## Build

```
make build     # npm run build (if web/node_modules exists) + go build → bin/kubit
make test
```

Requires Go 1.26+, Node 20+ for the UI. Talos machinery pinned to v1.14.0
(Kubernetes 1.37.0 default).

## Dev VMs

`brew install vfkit`, then:

```
hack/vm/vm.sh create 1          # 2 vCPU / 4 GiB / 20 GiB disk, boots Talos ISO (schematic with siderolabs/gvisor)
hack/vm/vm.sh list              # shows IP from vmnet's DHCP lease (192.168.64.0/24)
hack/vm/vm.sh start 1 --no-iso  # after Talos has installed to disk
hack/vm/vm.sh destroy all
```

Verified: EFI boot of the Talos v1.14.0 `metal-arm64.iso` under Virtualization.framework
reaches maintenance mode; the insecure `Version` call on :50000 answers
`tag=v1.14.0 arch=arm64 platform=metal`. The API is flaky for roughly the first two
minutes after boot (vmnet NAT settling; NTS lookups time out in the same window) and
stable thereafter — discovery must retry. No serial console output: the arm64 ISO
uses `ttyAMA0`, not the virtio console.

Image Factory schematic for `siderolabs/gvisor` (arm64 and amd64):
`d9ff89777e246792e7642abd3220a616afb4e49822382e4213a2e528ab826fe5`.

## Status

- [x] Phase 0 — scaffold, `kubit version`, `kubit serve` (SPA + `/api/v1/version`), VM harness
- [ ] Phase 1 — store, keyring crypto, `cluster.yaml`, machine config generation
- [ ] Phase 2 — Talos client, `kubit discover`
- [ ] Phase 3 — `kubit cluster create`, `kubit node add`
- [ ] Phase 4 — platform add-ons via OpenTofu
- [ ] Phase 5 — Day-2: remove node, Talos/K8s upgrades, telemetry
- [ ] Phase 6 — export
- [ ] Phase 7 — web UI
- [ ] Phase 8 — iPXE
