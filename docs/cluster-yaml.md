# Cluster repo

A cluster is a directory in a git repo you own. This page describes what is in it and what
every field of `cluster.yaml` does.

## Files

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
      repository: { url: ssh://git@github.com/you/apps.git, branch: main, path: ./clusters/lab }
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
(shown with *Copy* in Settings › Apps); see [Secrets](console.md#secrets).

**Private apps repo (deploy key).** `platform.flux.repository.url` is an `https://` URL
for a public repo or an `ssh://user@host/path` URL for a private one (the scp form
`git@host:path` is refused; Flux takes only ssh:// URLs). Connecting the cluster to its
[apps repository](apps-repository.md) sets the URL. For ssh, connecting (or *Generate* in
Settings › Apps, or `kubit deploy-key lab`) writes a new ed25519 key to `secrets.sops.yaml`
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
may sleep; a failure shows as the object's message on the Flux card.
