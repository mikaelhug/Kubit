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
