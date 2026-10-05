# Backlog

- Forget cluster leaves `~/.kubit/clusters/<name>/` behind (admin `kubeconfig`,
  `talosconfig`, `snapshots/`, `infra/`), although the dialog says it deletes the
  talosconfig and kubeconfig. Seen 2026-09-28 after forgetting `lab`. Decide: delete the
  directory (keep snapshots only if the user asks), and make the dialog match.
- vfkit console: arm64 ISO logs to ttyAMA0; direct-kernel boot with `console=hvc0` would
  give a readable `console.log`.
- vmnet NAT is flaky for ~2 min after VM boot; harness could poll :50000 before returning.
- `status` etcd leader detection only asks the first reachable control plane; a
  cluster whose first CP is down reports no leader.
- Stale mirror pods (`Pending`, old image) linger in kube-system for a while after a
  Kubernetes upgrade; harmless, but the dashboard could surface component versions
  from the API server rather than pod images.
- ingress-nginx Helm upgrades occasionally fail on the `ingress-nginx-admission-patch`
  post-upgrade hook (BackoffLimitExceeded); the retry succeeded. Consider disabling the
  patch job (`controller.admissionWebhooks.patch.enabled=false` with cert-manager
  issued webhook certs) or an automatic single retry of failed platform applies.
- Add-on readiness for metrics-server counts all of kube-system; scope by the release's
  label selector instead.
- PXE end-to-end is UNVERIFIED. On the Mac harness `kubit pxe` could not bind :67 next
  to macOS's bootpd (vmnet's DHCP); :67/:4011 now use SO_REUSEPORT but whether
  Apple's EFI network-boots at all is unknown. Test on the HP EliteDesks (real L2,
  router DHCP, `sudo kubit pxe --iface <lan-if>`); the PXE page and boot tracker are in
  place and only need traffic.
- Re-address flow: `machine.ip-changed` after a real DHCP lease change is only
  unit-tested; vmnet-helper leases are sticky per MAC. Verify on hardware (release the
  lease on the router) and confirm the Nodes page offers *Update address*.
- Wizard pool editor offers a static list of common Image Factory extensions; fetch
  `/extensions` (or the schematic API's official list) per Talos version instead.
- Wizard Design step lints only on Review; live per-cell warnings (disk too small for
  the pool's selector, hostname collisions) would be friendlier.
- Re-addressing a control plane reboots it (etcd/static pods bind the old address); a
  gentler path would restart etcd + kubelet services only, if Talos updates the etcd
  peer URL on service restart. Not investigated.
- SMTP forwarding verified only in plain mode against a local receiver; STARTTLS and
  implicit-TLS paths need a run against a real provider (Gmail/Fastmail app password).
- The `master.key` fallback has unit tests but no run on a real Linux host yet.
- Off-site S3 target untested against a real bucket (MinIO in a container would do);
  the directory target is verified. Restoring *from* the off-site copy is manual today
  (download the `.kubitbak`/snapshot and use `kubit restore` / the Backups tab) — a
  "restore from off-site" button would close the loop.
- Events with an empty cluster (Kubit backup failures) are forwarded but not shown in
  any cluster's Overview; a small "Kubit" notice on Settings or the status bar is missing.
- Workload alert rules are fixed (5 min age gate, 3 restarts / 10 min); make them
  per-cluster settings if a workload legitimately restarts often (e.g. batch jobs).
- Info-level events (node.ready, api.back, …) accumulate unacked in the events table;
  they are hidden from the alert list but the count grows. Auto-ack info events
  older than a day.
- Restore of a control plane whose `ip:` has changed since the snapshot was taken is
  not handled (snapshot carries the old Node objects; kubelets re-register, but the
  old Node entries linger until deleted).
- Maintenance window: the daemon's own scheduled snapshots ignore it on purpose
  (non-disruptive); consider a per-cluster switch for "quiet hours" on alerts.
- Kubit's old default :8080 collided with another local app during M9 testing; the
  default is now :8090. Consider a free-port fallback with a clear log line, or making
  the port part of Kubit settings.
- Maintenance-window notice in confirm dialogs is fetched when the dialog opens (not
  live); computing open/closed client-side from the spec would remove that fetch.
- The live ring buffer (2000 messages) is per daemon process; a long outage still ends
  in a resync, which is correct but reloads everything. Fine at this scale.
- AMT: verify Probe/Power/BootPXE against a real vPro box (first run, then `Boot into
  Talos` end to end with `kubit pxe` running). If BootPXE's BootSettingData Put is
  rejected by older firmware (AMT < 11), fall back to ChangeBootOrder + SetBootConfigRole
  alone. KVM (VNC over AMT) is not implemented.
- IDE-R (virtual CD over AMT) was implemented in M18 and reverted. Its EliteDesk
  verdict ("the CD boot cannot be forced") is unproven: every forced boot until
  2026-09-16 referenced the boot configuration as `Intel(r) AMT: Boot Configuration
  Setting 0`, which AMT answers with `4 Invalid Reference`, so the `IsNextSingleUse`
  role was never set and the BIOS ignored every override — PXE and CD alike. What
  still stands: `AMT_BootCapabilities.ForceCDorDVDBoot=false` and `CIM_BootSourceSetting`
  listing only Force Hard-drive and Force PXE. The code lives in commit 7e278d2
  (`internal/oob/ider`, `internal/boot`, `kubit boot`) plus the stash "ide-r
  diagnostics wip" if it is ever retried with the fixed role call.
- Lab host, EliteDesk run 2026-09-16: partman, bridge and netboot paths all worked
  first time. Left over: the host came up as `DESKTOP-DEV86LM` (the router's DHCP
  name for the MAC won over `netcfg/hostname`; pass `netcfg/get_hostname` on the
  kernel line in the PXE path too, as the manual boot line already does); *Make lab
  host* accepts a VM plan the host cannot hold (4×3 GiB on 7.7 GiB) and only finds
  out after the install — cap the plan by a RAM guess (AMT exposes none) or let the
  dialog say the plan is checked after install more prominently. The VM table shows
  no address for bridged VMs (libvirt has none; the machine row has it) — show the
  row's IP there. Resize applies on next boot only; no live migration; no multi-host
  scheduling.
- Lab VM start on a bridge (EliteDesk, 2026-09-17): `virsh start` for the first VM
  enslaves its tap to `br0`, which briefly resets the host's uplink; because the
  host's management IP lives on `br0`, the in-flight SSH command dies with an SSH
  `ExitMissingError` ("exited without exit status") although the VM does start. The
  labhost client now re-dials on a dropped control connection and `Start` reconnects
  and confirms the domain reached `running` instead of failing the provision
  (`internal/labhost`). Open: if the reset also changes the host's DHCP lease/address
  the reconnect to the old IP fails — re-discover the host by MAC then. Routed VM
  networking (separate `kubitbr0`, host IP untouched) avoids the blip entirely and is
  the alternative if bridged proves too flaky.
- Provisioning deep review (2026-09-17): a 5-reviewer adversarial pass over the whole
  chain fixed S1-S3 issues — the S1 re-image (a ready lab host kept `provision=1` and
  `pxeDecision` served Debian; now cleared on setup, `labBoot` gated to `installing`,
  and the ready->local branch moved ahead of the arm flag), the disk-boot boot-loop
  (`SetDiskBoot`/`SetTalosBoot` now assert the XML changed and are set BEFORE the apply
  reboot, failure fatal), `WaitForReboot` fast-fails a maintenance reboot, a preflight
  control-plane RAM floor (2 GiB), PXE fail-closed on a daemon outage, download
  integrity + detached ctx, and the lab-host state-race root cause (`store.UpdateLabHost`
  per-host merge used by the watcher/maintenance/resize/delete; per-host op locks; a
  restart reconciler; partial-VM-leak reap; fresh-read overcommit). Deferred (lower
  severity, some need hardware/Talos verification):
  - resume can't find a DHCP node whose lease moved between a failed run and the retry
    (`create.go pendingInstall`) — re-discover by MAC like `labAddVMs` does.
  - VLAN branch emits no explicit parent-link `up` (`generate.go`) — verify whether the
    target Talos release brings a VLAN parent up implicitly before adding a LinkConfig.
  - NetworkStep does not validate per-node static address/gateway/VLAN inline; a bad CIDR
    is only caught at Create (`web/src/pages/create/steps.tsx`).
  - PXE tracker attributes an HTTP fetch to the most-recent DHCP client without an IP
    (telemetry only under concurrent boots) — attribute by MAC via the asset URL.
  - `labhost.MAC(host,n)` caps host and index at 255; timeouts are process-global not
    per-cluster; the labhost SSH host key is not pinned after first contact.
- Lab cluster role assignment was wrong (EliteDesk, 2026-09-17): `config.Design`
  picks the *smallest* machine as control plane (right for a mixed bare-metal fleet),
  but `labDesign` then took the first N of that order, so the control-plane role
  landed on a 1 GiB worker VM instead of the 2 GiB VM the plan sized for it — etcd
  bootstrapped but 0/3 nodes ever went Ready and the API server died. `labDesign` now
  assigns roles by the VM's planned role (matched by MAC) and points the endpoint at a
  control-plane node (`internal/api/labhost_design.go`, regression test
  `TestLabDesignControlPlaneIsThePlannedVM`). Open: no path to re-run VMs+cluster on an
  already-installed lab host without re-PXE — a fix iteration reinstalls Debian.
- Lab-host provision is now preflighted and self-cleaning (2026-09-17): before it
  arms anything it refuses when the PXE server is down, on a different /24 than the
  machine's AMT (`pxe-segment`), or when AMT will not answer (`amt-down`); on any
  failure or cancel the operation releases the host (VMs deleted, record and arm
  cleared, machine back to `configured`) instead of leaving a dangling `error`
  record, and the reason shows in Activity. `internal/api/labhost_provision.go`. Residual: the
  pxe process -> daemon link (`--kubit-url` for `/pxe/decide`) is not preflighted; a
  wrong URL makes the proxy serve Talos to everything. The old failures were timing —
  the PXE server started after the arm / was restarted mid-provision (wiping its boot
  tracker), or the op was cancelled before the box's ~90s-late boot, so it booted
  un-armed and got Talos. Keep `kubit pxe` up (ideally the root service) across a run.
- Talos boot assets on a lab host could be silently truncated: `EnsureTalosBoot`'s
  command ended in `ls -l`, so a failed/partial `curl` (initramfs came back 0 bytes on
  the EliteDesk after a transient) still returned success, and the VMs booted nothing
  and hung "booting". Now each file is fetched to `.part`, renamed on success, and an
  empty result is a hard error (`internal/labhost/libvirt/client.go`); a stale 0-byte file
  self-heals on the next setup because `-s` re-triggers the download.
- Discovery and AMT: a scan of the LAN also finds the engine's own lease of a known
  machine; the row now keeps its OS address and only `oob.host` moves. Still open:
  `discoverAMT` rewrites the machine's sealed AMT credentials with the settings'
  defaults whenever the defaults work — per-machine credentials should win.
- Lab host without AMT (USB-installed Debian): add "adopt existing host" that only
  needs SSH access with Kubit's key.
- Lab host updates: VMs defined before M16 lack `virsh autostart`; `labhost.update`
  restarts what was running, but an unplanned host reboot leaves them off. A one-off
  `virsh autostart` sweep on the first tick would fix existing hosts.
- Lab host metrics: per-VM actual qcow2 usage (`qemu-img info` → actual-size) would
  tell which VM is eating the disk when `labhost.disk-low` fires.
- Lab host: `hack/seedlab` fills a scratch `KUBIT_HOME` with a fake host for UI work;
  it is dev-only and not wired into the build.
- Storage consumer for data disks: opt-in `localPath` platform add-on (rancher local-path-provisioner with `nodePathMap` → `/var/mnt/data-*`) so PVCs land on the data volumes without hostPath; Longhorn/OpenEBS stay user-deployed.
- Hardware tab for AMT-only machines: state that inventory arrives once the machine boots Talos (AMT exposes no disks/RAM); the row is otherwise blank.
- Data-disk selectors by serial/WWN instead of dev_path (Talos `disk.serial`): dev paths can shift on multi-controller boxes; inventory would need the serial first.
- Lab harness: the installer and the installed system take different vmnet leases
  (different DHCP client ids), so `lab.sh route` must be re-run after the install;
  Kubit itself copes (the SSH phase re-reads the row and the progress report carries
  the current address). A fixed lease per MAC in vmnet would remove the step.
- Lab hosts: the routed network's subnet (192.168.123.0/24) is fixed; make it a
  setting if a site already uses it.
- Boot into Talos and the lab install share `labWaitBoot`; the same phase machine
  could serve `discover` when it waits for a PXE-booted machine.
- The config draft (`draft`, `internal/api/clusters.go`) falls back to `installDisk: /dev/sda` when a
  machine has no inventory; wrong on NVMe-only boxes. Leave the path empty (pool
  policy / selector) or refuse instead of guessing.
- Lab host hardware record: refresh from Debian in `labTick` (lsblk/dmidecode) so the
  Hardware tab stops showing the pre-install Talos scan or the AMT stub.
- Retiring a lab host's row after release leaves its VM rows orphaned (`host` points
  at a missing MAC); retire allows them one by one, a sweep would be kinder.
- `writeErr` now maps gRPC Unavailable/DeadlineExceeded to 502/504 (was 500); scripts
  matching on 500 would notice.
- Redfish: verify against a real BMC (iDRAC/iLO): `PermanentMACAddress` ordering,
  whether `Boot.BootSourceOverrideMode=UEFI` must be PATCHed alongside `Pxe` on that
  vendor, and Storage→Drives visibility for NVMe behind a plain PCIe slot.
- Identity: viewer/operator roles are enforced by the API only; the UI still renders
  every action button and shows a 403 toast. Hide or disable by `can(role)` per page.
- Identity: OIDC verified only against the in-process fake provider; run once against
  Keycloak/Entra (groups claim name differs: Entra sends object ids unless configured).
- Identity: `kubit pxe` needs an API token once accounts exist; it should get one
  without `KUBIT_TOKEN` set by hand.
- QEMU lab (`hack/qemu`): written on macOS and has not booted a VM yet (OVMF path,
  tap ownership, dnsmasq lease file permissions are the likely first fixes). GitHub
  Actions removed; signed releases would need a new home if ever wanted.
- Longhorn: unverified on a cluster. Check that Talos propagates `/var/mnt/data-N`
  user volumes into the kubelet with shared propagation (needed for Longhorn's
  bind mounts); if not, a UserVolumeConfig-based `longhorn` volume or a v1alpha1
  kubelet mount fallback is needed. Add a Storage-tab panel reading
  `longhorn.io/v1beta2` volumes (health, replicas, backup target).
- Platform apply: retry the whole tofu apply with backoff on transient API errors and
  scale the Helm timeout with node count (reliability plan P0, still open).
- Smoke test at the end of create: a tiny Deployment + LoadBalancer Service must get an
  IP before the cluster is reported ready (reliability plan P2).
- cert-manager still ships without resource requests; give it the same treatment
  before enabling by default (Flux's chart sets 100m/64Mi per controller).
- Observer (2026-09-19): Kubit lives on a laptop by design; sleep is handled (gaps
  re-baseline, `backup.stale` skips slept time). Open: the WebSocket reconnect after a
  long sleep always ends in a `resync` (fine); a per-machine "last contact" for
  maintenance-mode rows comes from discovery's `lastSeen` and is not labelled as such;
  the macOS per-process LAN denial seen on 2026-09-19 (an orphaned `kubit serve` got
  `EHOSTUNREACH` for every LAN address while other processes did not) is detected and
  reported but its cause is not proven.
- Console rework leftovers (2026-09-19): Inventory's *Boot all into Talos* fires one
  power op per machine (no single bulk operation); the lab host Hardware tab shows host capacity
  only, no disks or links until Debian reports them (`labTick` refresh from lsblk is
  still open above); Kubit-level events (`kubit` pseudo-cluster: test alert,
  heartbeat) are not listed anywhere in the console.
- Lab host on this Mac (vfkit, 2026-09-25) leftovers:
  - Firmware reboot (`talosctl reboot --mode powercycle`) is unverified; Virtualization.framework
    likely stops the VM (vfkit exits 0, same as a guest shutdown), so it shows `shut off`
    until started. If that bites (Talos upgrades without kexec), restart VMs whose
    `spec.json` has `run: true` from the watcher tick, not only at daemon start.
  - `caffeinate -i` covers `labhost.local` only; the nested `cluster.create` can still be
    interrupted by a lid-close sleep. Laptop sleep during setup is untested.
  - Memory alert on a Mac: `vm_stat` used (active + wired + compressed) sat at 75 % with
    6 GiB of VMs on a 24 GiB machine; `labhost.memory-pressure` (92 %) may be noisy on a
    busy desktop. Consider macOS's memory-pressure level instead.
  - VM disk % on APFS is the whole container (total − available), not the VM files.
  - Old Talos ISOs under `~/.kubit/vms/boot/` are never pruned; the serial console is
    empty (arm64 ISO logs to ttyAMA0); Intel Macs untested (amd64 path exists).
  - Runbook text for `labhost.unreachable` and the Home empty state still speak of SSH/Debian.
  - README Endpoints list is stale (machines, oob, labhost, labhosts routes missing).
- Hyper-V lab host (feasibility, no Windows machine yet): fits the `labhost.Driver` seam.
  Kubit stays on macOS/Linux and drives Windows over its built-in OpenSSH server with
  `powershell -EncodedCommand` returning JSON (Kubit's ed25519 key in
  `administrators_authorized_keys`). Gen 2 VMs, Secure Boot off, static memory, the
  factory ISO on a DVD (downloaded by the host with `curl.exe`), `SetDiskBoot` via
  `Set-VMFirmware -FirstBootDevice`, static MACs from `labhost.MAC`, no `Updater`.
  Networking: an External vSwitch on Ethernet (internal switches have no DHCP; Wi-Fi
  bridging is unreliable). Talos has no KVP daemon, so addresses come from the existing
  subnet scan by MAC. Blockers: `designDisks` and the web `installCandidates` pin
  `/dev/vda`, but Hyper-V SCSI disks are `sda`/`sdb`; needs Windows Pro/Server.

- Namespace management (create/delete an app namespace with a Pod Security level) is left
  out on purpose: app namespaces live in Git with the app. Revisit only if Kubit ever
  deploys apps itself. The node page's pod table still lists every namespace (platform pods
  matter there); a scope toggle could follow if it gets long.
- Full lab run (2026-09-26) leftovers:
  - Machine rows of cluster members get `source: manual` from `storeRow` on every
    create/add (the scan/lab/amt provenance is overwritten). Unused anywhere today; fix
    by keeping an existing non-empty source in `UpsertNode`.
  - `api.unreachable` is raised during Kubit's own single-control-plane upgrade (the API
    is down while the only control plane reboots). Correct, but it could be marked as
    expected while an `upgrade.*` operation runs.
  - Settings → *Apply node configs* runs at once with no confirmation, though a config
    change can reboot nodes; a dialog stating "may reboot nodes one at a time" would fit.
  - Lab VMs keep the Talos ISO attached (read-only `sda`) until their next cold start;
    harmless, visible on the Hardware tab.
  - Longhorn has no *Open* link: its UI service is ClusterIP. An Ingress or a
    LoadBalancer toggle in its Configure dialog would expose it.
  - Longhorn enabled while a node had no data disk at the time: Longhorn never adds the
    disk later. A new data disk on an existing node needs the disk added in Longhorn
    (or the node object recreated); Kubit could patch `nodes.longhorn.io` after Apply.
  - Lab host disk metrics on macOS measure the whole APFS container, so the 85 % disk
    alert can fire from unrelated files on the Mac.
- App secrets (M23) follow-ups:
  - External Secrets Operator add-on with provider presets (Bitwarden Secrets Manager,
    Doppler, Infisical); Kubit keeps the provider's bootstrap token sealed and installs
    it like the SOPS key. For secrets that must come from a manager, not Git.
  - Key rotation in the UI: keep old and new identities in `keys.txt` during the
    rollover, show both recipients, drop the old one on confirm.
  - Private and SSH apps repositories: a deploy key Kubit generates, seals and installs
    as the GitRepository's `secretRef`.
  - Platform secrets in add-on values (ACME DNS tokens, Longhorn S3 credentials) are
    plaintext in cluster.yaml, readable by viewers and copied into tfvars/tfstate.
    Needs sealed values with viewer redaction.
  - tfvars and tfstate are plaintext on disk (0600, inside `~/.kubit`; sealed only in
    backups).
  - The Flux card shows the recipient Kubit holds, not whether the cluster's
    `flux-system/sops-age` still matches it; a drift check could compare the two.
- Builds follow-ups: rootless BuildKit (needs `user.max_user_namespaces` > 0 on the
  nodes), TLS and auth for buildkitd and the registry (any pod can push), registry garbage collection of old tags, private Git repositories (a deploy
  key), build logs streamed in the UI instead of the raw log link, rebuilds on source
  changes without a version bump.
- Platform apply: a Deployment that never rolls out (the registry on the first M25
  run) blocks `tofu apply` for 10 minutes with no progress line; Kubit could surface
  the pod's events while it waits.
- The lab dialog and wizard take no sync interval, so a push lands within the 5 min
  default; *Sync now* (Flux follow-ups) would cover demos.
- Storage on the system disk: Kubit does not detect an existing node whose EPHEMERAL
  already fills the disk (the `data-system` volume then stays unprovisioned); the node
  page could show the live volume status.
- Flux (M24) follow-ups:
  - *Sync now* on the Flux card: annotate the GitRepository with
    `reconcile.fluxcd.io/requestedAt` instead of waiting for the interval.
  - Push webhook: a notification-controller Receiver behind ingress, its token sealed
    by Kubit.
  - Multi-tenancy lockdown: the root Kustomization applies with cluster-admin, so the
    repository can touch platform namespaces.
    A service account with namespace-scoped rights, or the chart's `multitenancy`.
  - Per-app Kustomizations need a hand-written `flux/<app>.yaml` next to each folder;
    a generator (Flux Operator ResourceSet, or Kubit templating one per folder)
    would drop the boilerplate.
  - Changing `repository.path` prunes everything under the old path before the new
    Kustomizations re-create it (volumes lost). The Configure dialog should warn, or
    the root Kustomization could be switched with pruning suspended for one apply.
  - The Flux card lists every object; with many apps it wants grouping per app or
    hiding Ready rows.
- Security follow-ups (phase 1 review):
  - `/api/v1/labhost/progress` is unauthenticated and its `ip` parameter moves a lab
    host's address; bind it to the armed MAC with a one-shot token from the preseed.
  - The lab-host SSH client ignores host keys (`InsecureIgnoreHostKey`); pin the key
    seen on first contact and refuse a change.
  - JSON handlers accept any Content-Type (hack scripts post with `curl -d`); requiring
    `application/json` would close form-post CSRF from other origins.
  - Bodiless POSTs (`daemon/stop`, `machines/{mac}/wake`, `events/{id}/ack`) are not
    covered by a Content-Type rule, and loopback without accounts is admin; check
    `Origin`/`Sec-Fetch-Site` on non-GET `/api/` requests in `ServeHTTP`.
- Review follow-ups (phase 2):
  - `platform.argocd` is still parsed as `LegacyArgoCD` and folded into Flux on load;
    dropping the field needs a store migration that rewrites stored specs first.
  - Locks held across remote I/O: the pools edit calls `EnsureSchematic` (Image
    Factory) under the spec lock; lab VM resize/delete hold `labhost:<mac>` across SSH.
  - `handleLabRelease` returns on a partial error before writing the audit entry.
  - WebSocket `OriginPatterns` is same-origin plus the dev server; a reverse proxy that
    rewrites `Host` fails the check. Accept `X-Forwarded-Host` or a configured origin.
- Lab host metrics: one `virsh domstats --list-running` call could replace the per-domain
  `virsh domstate`/`dumpxml`/`qemu-img info` loop in `libvirt.Client.List` (needs a real
  libvirt host to verify the field mapping).
- Rename leftovers: a failed rename after the new hostname registers leaves the old
  cordoned Node object; the error names it. Marking it (annotation) would let a later
  pass delete it safely.
- A Debian mirror without ETags can still answer 304 for an older-dated `current`
  file; deb.debian.org sends ETags.
- Node remove leaves the removed node's subnet in the other nodes' firewall rules until
  the next Apply, and when it was the only node of that subnet config status reports the
  remaining nodes behind. Harmless (slightly wide); re-apply after remove, as re-address
  does with its `narrow` step.
- Node add's `firewall` step applies each existing node's full regenerated config, so a
  pending declaration change (an unapplied patch) reaches them too, and one unreachable
  node fails the add. Apply only the firewall documents, merged with the running config.
- `CheckCluster` runs on every edit, so a cluster whose generation already fails for an
  unrelated reason refuses every save with 422 until fixed. Watch clusters stored by
  older versions; offer a YAML save that skips the check for repairs.
- Host firewall hint "Blocks node ports from outside the cluster" (wizard, Settings) is
  broad: apid and the API stay open. Use "Only cluster traffic reaches node ports".
- Patches are YAML only. Add a per-cluster/pool/node patch editor with the 422 message
  shown inline.
- `POST /clusters` (create) checks patches only when the operation generates; the wizard
  calls `/config/validate` first. Run `CheckCluster` in the create handler too.
- Host firewall unverified on Talos: `talosctl get nftableschains`, `kubectl top nodes`
  (pod→kubelet), a LoadBalancer Service (MetalLB memberlist) and a node add from a
  second subnet on a lab cluster with the firewall on.
- Add-node dialog does not mark TPM-less machines when the cluster is TPM-encrypted; the
  add is refused by validation. Grey them out with the reason.
- `POST /clusters/{name}/nodes` does not fill `tpm`/`watchdog` from inventory when the
  body omits them (dialog and CLI do). Fill from the machine row like the CLI.
- Declarations with `encryption: tpm` and a node without `tpm: true` no longer parse;
  only possible for declarations saved by an interim build before TPM validation landed.
  Intended; remember it if a stored cluster will not load.
- Existing nodes have no `watchdog`/`tpm` keys. A "refresh hardware flags from
  inventory" action could fill `tpm`/`watchdog`/`kvm` on declared nodes.
- Disk encryption, TPM and watchdog detection unverified on hardware: confirm `LS` sees
  `/dev/tpmrm0`, `/sys/firmware/efi`, `/dev/watchdog0` in maintenance mode (any error
  but NotFound counts as present), `volumestatus` shows luks2, `nodeID` volumes unlock
  after reboot, `watchdogtimerstatus` is armed and a hung node resets.
- Secure Boot installs are the prerequisite for making `tpm` the default: Talos 1.14
  sealing reads the PCR signing key only signed UKIs carry. Needs signed images from the
  Image Factory, key enrolment on the machines, and a PXE path that boots signed UKIs.
- `ConfigStatus` regenerates every node's config on each fetch (per cluster row write
  while a view is open). Cache by spec hash plus machine-config write if it shows up.
- Apply's manifests step reads bootstrap manifests right after the last node; if Talos
  has not re-rendered them yet the old ones are synced and NetworkPolicy enforcement
  waits for the next Apply. Lab check: turn `policies` on, Apply, look for the
  `kube-network-policies` DaemonSet; wait for the manifest version if it lags.
- `cluster.apply` stops before the manifests step when a node fails; a retry re-applies
  every node first. Resume at the failed node instead.
- `kubit status` does not print which nodes are behind the declaration. Add a line from
  `ConfigStatus`.
- `node.upgrade` now stores the config it applies but is not in `configKinds`
  (`internal/api/live.go`), so the `config` scope does not refresh after a single-node
  upgrade. Add it.
- Turning `network.policies` off stops Talos from rendering kube-network-policies, but
  the DaemonSet already in the cluster keeps enforcing. Prune it in the manifests step
  (server-side apply does not delete) when policies go off.
- Failed drain after a Talos install leaves the new image in the inactive slot; the node
  boots it on any later reboot. Show "installed, not booted" in Lifecycle (boot entry vs
  `readNodeImage`) or offer *Reboot without drain* from the failure.
- A node left cordoned by a failed upgrade looks like an operator's cordon, so a retry
  leaves it cordoned and the cluster precheck refuses it. Mark Kubit's cordon with an
  annotation and uncordon only those on retry.
- Drain timeout (5 min) and PDB policy are hardcoded; single-node clusters and Longhorn's
  last-replica PDB can block a drain. Offer *skip drain* / *disable eviction* per run.
- `LifecycleService.Upgrade` lacks the etcd guard the legacy `MachineService.Upgrade`
  (force=false) ran on each control plane; precheck and `waitBack` cover the run
  boundaries but not a member that turns unhealthy mid-run. Check etcd health before
  each control plane's drain.
- `configBehind` relies on byte-identical `config.Generate` output. Add a unit test that
  two Generate calls with the same bundle are equal so upgrades never flap "behind".
- No linter catches deprecated machinery calls; run `staticcheck` (SA1019).
- Lifecycle/Image API upgrade unverified on a node: installer pulled into `NS_SYSTEM`,
  the digest-pinned name accepted as `ImageName`, a plain reboot (and kexec) boots the
  new slot, the pre-reboot config apply on the old OS, install log volume.
- Containerd config v4 arrives with Talos 1.15; check Kubit's `RegistryMirrorConfig` and
  any containerd patches against it before allowing 1.15 targets.
- Pushed stage: a tick whose probe started before a pushed change stores the older
  stage, restarts that watch, and the fresh watch patches it back (one extra
  `status`/`nodes` pair). Have the tick consult the watch's last-seen time first.
- `OnStatus` side effects (snapshot scheduling, cert checks, off-site, heartbeat) also run
  on pushed statuses. Harmless (gated), but a publish-only hook would be clearer.
- A node reachable on :50000 that refuses the COSI stage watch logs once per tick. Add a
  per-node backoff or log-once.
- Pushed stage unverified on a node: a reboot/upgrade on the lab should move the stage
  pill without waiting for a tick, and a sleep/wake should log no stale pushes.
- Gateway API objects (Gateway, HTTPRoute, GRPCRoute) are not in the Network view and
  raise no alerts (`ingress.no-address` covers Ingress only). List them, and alert on a
  route no Gateway accepts or a Gateway without an address.
- The empty `ingress-nginx` namespace stays after migration (Helm keeps namespaces). Add
  an explicit, reviewed cleanup step.
- Traefik's own CRDs come from the chart's `crds/` and are never upgraded by Helm. A
  chart bump that changes them needs a CRD apply step like the Gateway API CRDs.
- Gateway API CRDs owned by another manager (Flux, another controller) fight with
  Kubit's force-conflicts apply. Add a setting to skip Kubit's CRDs.
- The plan view shows full CRD bodies (`yaml_body_parsed`) on the first apply; fold or
  summarise CRD resources.
- The default Gateway listens on HTTP only; HTTPS listeners need `certificateRefs`
  through values. Offer a cert-manager-backed HTTPS listener when cert-manager is on.
- A failed plan/apply clears `ingress_ip` in platform status, which raises `lb.lost`
  while the address is in use. Keep the outputs on error (and drop the tfvars pin
  fallback).
- `metrics-server.tf` is not `tofu fmt` clean (alignment of `=`). Run `tofu fmt`.
- Cilium as a create-time CNI option (Flannel + kube-network-policies stays the
  default); needs `cni: none`, a Cilium chart in the platform layer before nodes go
  Ready, and kube-proxy replacement as a choice.
- Kubelet serving-certificate rotation (`rotate-server-certificates`) would let
  metrics-server drop `--kubelet-insecure-tls`; it needs an in-cluster CSR approver.
- Wake-on-LAN through Talos `EthernetConfig` (enable WoL on the NIC) needs the kernel NIC
  name, which the uplink alias does not give; record it from inventory first.
- A pushed stage `OnStatus` and a tick `OnStatus` can be published out of order; the next
  tick corrects it. Stamp statuses with their probe time and drop older ones on publish.
- Re-address, move to pool and rename push the full regenerated config to a control plane
  without syncing the bootstrap manifests, so a pending manifest change (such as
  `network.policies`) waits for the next Apply. Run the `manifests` step after them when a
  control plane was applied, as Talos upgrade and node add do.
- Rename does not uncordon when its drain fails (only when the apply fails). Uncordon on
  any failure before the apply, as the upgrade drain does.
- Retrying a failed `node.add` operation (`ops.go`) skips the trial generation the
  `POST /clusters/{name}/nodes` handler runs. Run `checkNodeAdd` there too.
- `upgrade kubernetes` refuses a node Kubit cordoned during an interrupted Talos upgrade
  ("uncordon before upgrading"); only `upgrade talos` resumes and releases it. Release
  Kubit's cordon in `ApplyConfigs` too, or say in the message to rerun the Talos upgrade.
- The etcd check before a control plane goes down reads Talos's etcd service health,
  which refreshes every 20 s, so a member that failed moments earlier still counts as
  healthy. Add a direct quorum read per member if that window matters.
- A Talos upgrade that fails mid-run leaves the upgraded nodes with stored configs for
  the new version while the declaration keeps the old one, so they show "Config behind"
  and Apply would push the old version's configs back. Point the notice at resuming the
  upgrade, or save the target version before the first node.
- Traefik migration without an address pin (MetalLB off or the old IP outside the
  range): the `nginx` IngressClass is created once Traefik is up, unordered against the
  ingress-nginx uninstall, which deletes its own `nginx` class. If the uninstall is
  slower, the class is gone while tofu state keeps it. Re-plan after the migration, or
  recreate the class in a second apply step.
- Apply node configs now fails at the manifests step when the Kubernetes API does not
  answer, although every node config was applied; the CLI then skips the platform apply.
  Report it as a warning with a retry hint instead of failing the operation.
- `checkNodeAdd` (API) and `AddNode` place the new node in the declaration with
  different matching rules (hostname and IP vs hostname or IP). Share one helper.
- The status-bar uptime keeps ticking from the last `hello` while the daemon is
  stopped or unreachable; show it only while connected.
- Lab host storage follow-ups:
  - Reinstalling Debian while another disk still holds an older `<hostname>-vg` can collide
    on the volume group name; zero the planned disks in the partman early command or set
    a unique `partman-auto-lvm/new_vg_name`.
  - The host's lvm2 may auto-activate LVM a guest wrote on a whole disk, which then
    blocks its wipe; filter passthrough disks in `lvm.conf` (`global_filter`).
  - A `labhost.pool-missing` alert for a storage disk in fstab that is not mounted (its
    VMs do not start).
  - libvirt re-provision keeps the system image or whole disk; Talos reinstalls over it,
    but a wipe would match vfkit.
  - The lab host's Hardware tab still shows the pre-install Talos scan; `capacity.disks`
    could replace it.
  - *Scan disks* attaches the inventory to the row of the NIC that PXE-booted; when the
    BMC reported another NIC's MAC, the dialog never fills in.
  - `hack/lab/lab.sh` could add a second virtio disk to exercise storage disks (virtio
    disks without a serial have no by-id link, so they use the device-path key).
- Lab VM sizing follow-ups:
  - `tune2fs -m 1` on Debian's root: ext4's 5 % root reserve is lost to VM images (93 GiB
    on a 2 TB disk); the sizing model assumes it stays.
  - *Add VMs* plans with a 40 GiB `/var`; it could take `ephemeralSize` from the cluster the
    VMs will join.
  - Mixed layouts (some VMs on whole disks, the rest on images) are never suggested.
  - The 2 GiB host reserve does not grow with the number of VMs (QEMU overhead per guest).
  - Size from Redfish-reported disks before a scan (sizes only, no device paths).
  - `hack/lab/lab.sh` still plans 20 GiB disks, which leave no room for `data-system`
    under a 40 GiB `/var`.
  - 6 suggested VMs raise the `schedulable-control-planes` info lint.
