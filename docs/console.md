# Console and API

The web console and its HTTP API are served by the Kubit daemon on your machine. Every view
is live, and every change it makes goes into the cluster repo, never around it.

## Running the console

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
  repo in a directory you type or pick with *Browse* (`cluster.yaml` designed from the machines' inventory,
  `secrets.sops.yaml` and `.sops.yaml` for your age key, created at
  `~/.config/sops/age/keys.txt` when missing; an existing `cluster.yaml` is never
  overwritten) and serves it, or appends the machines to a served cluster's `spec.nodes`
  with the next free hostnames. Both go through a review of the exact result first
  ([Discovery](operations.md#discovery)).
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
- **Settings › cluster.yaml** is an editor (YAML highlighting, ⌘S saves). A save is
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
Secrets and Settings; *Apply* creates a declared one.

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
| `/clusters/<name>/…` | Overview, Changes, Nodes, Workloads, Network, Storage, Add-ons, Secrets, Backups, Settings (cluster.yaml editor, Apps, Git, Certificates, Danger zone; kubeconfig) |
| `/machines/<mac>` | Overview, Hardware, Kubernetes, Services, Logs (`?tab=`) |
| `/discovery` | machines to add, machines waiting for apply, other machines, scan, PXE state |
| `/secrets` | the SOPS files of every served repo, each linked to its cluster's Secrets tab |

## API

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
result without writing), `POST design/checks` (the same with the VIP and address probes), `POST repos` (same body, writes a new repo), `GET dirs?path=` (subfolders, for *Browse*), `POST clusters/{n}/nodes`
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
type, stringData}`), `POST clusters/{n}/sops/flux[?repo=i]`, `POST clusters/{n}/destroy` (`{name}`, 202; lines arrive as `apply` messages),
`GET|POST clusters/{n}/flux/key`
(`{hash, hostsOnly}`; public key, fingerprint, host keys). Unknown `/api/`
paths answer 404 JSON.

## Secrets

A cluster's *Secrets* tab lists the Kubernetes Secrets Flux applies to it: the
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
for relative and absolute paths. Repos with a single shared rule show a notice on the cluster's
Secrets tab; *Let Flux decrypt* adds the Flux key to that repo's `.sops.yaml` (splitting a
shared rule) and re-encrypts the app secrets Flux can't read.

## Settings file

`kubit serve --config kubit.yaml` reads these (defaults otherwise):

```yaml
factoryUrl: https://factory.talos.dev
pxeStatusUrl: http://127.0.0.1:8069/status.json
alerts:
  minSeverity: warn
  webhookUrl: https://hooks.slack.com/…
discoverySubnets: [192.168.1.0/24]
```
