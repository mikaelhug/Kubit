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
  alone. KVM (VNC over AMT) and IDE-R remote ISO are not implemented.
- Lab host: first real run on the EliteDesk will shake out preseed details (partman on
  NVMe with an existing Windows EFI partition, the bridge interface name, Debian 13
  netboot paths). Keep `virsh console` handy. Resize applies on next boot only; no
  live migration; no multi-host scheduling.
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
