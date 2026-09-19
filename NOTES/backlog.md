# Backlog

- vfkit console: arm64 ISO logs to ttyAMA0; direct-kernel boot with `console=hvc0` would
  give a readable `console.log`.
- vmnet NAT is flaky for ~2 min after VM boot; harness could poll :50000 before returning.
- Two clusters on one L2 with the same MetalLB range collide (both announce .200); the
  create wizard could warn when a range overlaps another stored cluster's.
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
- `kubit service install` on Linux (systemd --user / --system) and the `master.key`
  fallback have unit tests but no run on a real Linux host yet; do it with the first
  Linux deployment (an always-on NUC is the intended home for the daemon).
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
- Kubit's default :8080 collided with another local app during M9 testing; the daemon
  was run with `--addr 127.0.0.1:8090`. Consider a free-port fallback with a clear log
  line, or making the port part of Kubit settings.
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
  control-plane node (`internal/api/labhost.go`, regression test
  `TestLabDesignControlPlaneIsThePlannedVM`). Open: no path to re-run VMs+cluster on an
  already-installed lab host without re-PXE — a fix iteration reinstalls Debian.
- Lab-host provision is now preflighted and self-cleaning (2026-09-17): before it
  arms anything it refuses when the PXE server is down, on a different /24 than the
  machine's AMT (`pxe-segment`), or when AMT will not answer (`amt-down`); on any
  failure or cancel the operation releases the host (VMs deleted, record and arm
  cleared, machine back to `configured`) instead of leaving a dangling `error`
  record, and the reason shows in Activity. `internal/api/labhost.go`. Residual: the
  pxe process -> daemon link (`--kubit-url` for `/pxe/decide`) is not preflighted; a
  wrong URL makes the proxy serve Talos to everything. The old failures were timing —
  the PXE server started after the arm / was restarted mid-provision (wiping its boot
  tracker), or the op was cancelled before the box's ~90s-late boot, so it booted
  un-armed and got Talos. Keep `kubit pxe` up (ideally the root service) across a run.
- Talos boot assets on a lab host could be silently truncated: `EnsureTalosBoot`'s
  command ended in `ls -l`, so a failed/partial `curl` (initramfs came back 0 bytes on
  the EliteDesk after a transient) still returned success, and the VMs booted nothing
  and hung "booting". Now each file is fetched to `.part`, renamed on success, and an
  empty result is a hard error (`internal/labhost/client.go`); a stale 0-byte file
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
- `handleClusterDesign` (server.go) falls back to `installDisk: /dev/sda` when a
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
- Identity: the PXE service needs an API token once accounts exist; `kubit service
  install --pxe` should mint one instead of relying on `KUBIT_TOKEN` by hand.
- CI: first run of ci.yml/release.yml/e2e.yml pending; the QEMU lab (`hack/qemu`) was
  written on macOS and has not booted a VM yet (OVMF path, tap ownership, dnsmasq
  lease file permissions are the likely first fixes).
- Longhorn: unverified on a cluster. Check that Talos propagates `/var/mnt/data-N`
  user volumes into the kubelet with shared propagation (needed for Longhorn's
  bind mounts); if not, a UserVolumeConfig-based `longhorn` volume or a v1alpha1
  kubelet mount fallback is needed. Add a Storage-tab panel reading
  `longhorn.io/v1beta2` volumes (health, replicas, backup target).
- Platform apply: retry the whole tofu apply with backoff on transient API errors and
  scale the Helm timeout with node count (reliability plan P0, still open).
- Smoke test at the end of create: a tiny Deployment + LoadBalancer Service must get an
  IP before the cluster is reported ready (reliability plan P2).
- ArgoCD/cert-manager still ship without resource requests; give them the same
  treatment before enabling by default.
- Observer (2026-09-19): Kubit lives on a laptop by design; sleep is handled (gaps
  re-baseline, `backup.stale` skips slept time). Open: the WebSocket reconnect after a
  long sleep always ends in a `resync` (fine); a per-machine "last contact" for
  maintenance-mode rows comes from discovery's `lastSeen` and is not labelled as such;
  the macOS per-process LAN denial seen on 2026-09-19 (an orphaned `kubit serve` got
  `EHOSTUNREACH` for every LAN address while other processes did not) is detected and
  reported but its cause is not proven.
- Console rework leftovers (2026-09-19): Home computes update notices from
  `api.versions()` per page load rather than the daemon's `versions` message carrying
  the latest Kubernetes too; Inventory's *Boot all into Talos* fires one power op per
  machine (no single bulk operation); the lab host Hardware tab shows host capacity
  only, no disks or links until Debian reports them (`labTick` refresh from lsblk is
  still open above); Kubit-level events (`kubit` pseudo-cluster: test alert,
  heartbeat) are not listed anywhere in the console.
