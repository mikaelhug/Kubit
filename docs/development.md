# Development

## Layout

```
cmd/kubit/   CLI (cobra)
internal/    api, cluster, config, factory, k8s, pxe, store, talos, tofu, watch, …
web/         Vite + Preact + TypeScript + Tailwind; dist/ embedded via go:embed
hack/vm/     vfkit harness: Talos arm64 VMs on Apple Virtualization.framework
hack/qemu/   QEMU/KVM lab for a Linux box (untested)
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

`hack/e2e.sh <subnet>` runs `kubit init`, `kubit apply` and a second plan that must be empty
against machines in maintenance mode on that subnet.
