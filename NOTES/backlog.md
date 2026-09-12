# Backlog

- vfkit console: arm64 ISO logs to ttyAMA0; direct-kernel boot with `console=hvc0` would
  give a readable `console.log`.
- vmnet NAT is flaky for ~2 min after VM boot; harness could poll :50000 before returning.
- vmnet NAT (vfkit) drops VM-to-VM traffic to a Talos Layer-2 VIP ("no route to host"
  from peers; the host itself reaches it). Control-plane VIP cannot be exercised in the
  harness; verify on real L2 (mini PCs / Proxmox). Same mechanism likely limits MetalLB
  L2 IPs to host→VM traffic.
