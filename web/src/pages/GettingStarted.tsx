import type { ComponentChildren } from 'preact'
import { useEffect, useState } from 'preact/hooks'
import { api, type Versions } from '../api'
import { latestTalos, machineList, settings } from '../store'

// Schematic with no extensions: enough to reach maintenance mode. Installs use the
// cluster's own schematic later, so the boot medium never needs to change.
const vanillaSchematic = '376567988ad370138ad8b2698212367b8edcb69b5fd68c80be1f2ec7d603b4ba'

/** First run: how machines get to Kubit, and the three steps to a cluster. */
export function GettingStarted() {
  const [versions, setVersions] = useState<Versions | null>(null)
  const machines = machineList.value
  const factory = settings.value?.factoryUrl ?? 'https://factory.talos.dev'
  useEffect(() => { api.versions().then(setVersions).catch(() => {}) }, [latestTalos.value])
  const talos = versions?.talos.find((v) => !v.includes('-')) ?? versions?.minTalos ?? 'v1.14.0'
  const iso = (arch: 'amd64' | 'arm64') => `${factory}/image/${vanillaSchematic}/${talos}/metal-${arch}.iso`
  const free = machines.filter((m) => m.state === 'maintenance' && !m.cluster).length

  return (
    <div class="p-8 max-w-3xl flex flex-col gap-6">
      <div>
        <h1 class="text-2xl font-semibold">Welcome to Kubit</h1>
        <p class="text-muted mt-1">Three steps from bare machines to a running Talos Kubernetes cluster. Kubit talks to the machines directly; nothing is installed on them until you say so.</p>
      </div>
      <Step n={1} title="Boot the machines into Talos maintenance mode" done={free > 0}>
        <p>Any mini PC, server or VM. Write the ISO to a USB stick (or attach it to the VM) and boot from it — Talos {talos} starts in memory and waits on port 50000. Nothing touches the disk yet.</p>
        <div class="flex flex-wrap gap-2 mt-2">
          <a class="btn btn-primary" href={iso('amd64')}>Download ISO · amd64 (Intel/AMD)</a>
          <a class="btn" href={iso('arm64')}>Download ISO · arm64</a>
          <a class="btn" href="/fleet/pxe">Network boot instead (PXE) →</a>
        </div>
        <p class="text-[12px] text-muted mt-2">Images come from the Talos Image Factory ({factory}); the installer used later carries the extensions your cluster declares.</p>
      </Step>
      <Step n={2} title="Discover them" done={free > 0}>
        <p>Scan the subnet from Inventory. Machines are identified by MAC, so they keep their identity across DHCP leases and reboots.{free > 0 && <> <b>{free}</b> unassigned machine{free === 1 ? ' is' : 's are'} already waiting.</>}</p>
        <a class="btn mt-2 self-start" href="/fleet/inventory">Open Inventory →</a>
      </Step>
      <Step n={3} title="Design and create the cluster" done={false}>
        <p>Pick the machines; Kubit proposes control planes and workers (bare metal first, odd etcd count), a VIP and a LoadBalancer range, and lints the design before anything is written. Add-ons (MetalLB, ingress-nginx, gVisor, metrics-server, cert-manager, Argo CD) are one toggle each.</p>
        <a class={`btn btn-primary mt-2 self-start ${free === 0 ? 'opacity-60' : ''}`} href="/clusters/new">Create a cluster →</a>
      </Step>
      <div class="panel p-5 text-[13.5px] flex flex-col gap-1">
        <h2 class="font-semibold text-[15px]">One capable box instead of several?</h2>
        <p>With Intel AMT on it, Kubit can install Debian + KVM on the machine and carve Talos VMs from it — a whole lab cluster on one PC. Scan the LAN, then choose <b>Make lab host</b> on the machine in the wizard.</p>
      </div>
      <p class="text-[12px] text-muted">Afterwards: the cluster runs on its own. Keep Kubit running (or install it as a service) for alerts, scheduled etcd snapshots and off-site copies; export at any time to manage the cluster without Kubit.</p>
    </div>
  )
}

function Step({ n, title, done, children }: { n: number; title: string; done: boolean; children: ComponentChildren }) {
  return (
    <div class="panel p-5 flex gap-4">
      <span class={`inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-full border text-[13px] font-semibold ${done ? 'border-good text-good' : 'border-accent text-accent'}`}>{done ? '✓' : n}</span>
      <div class="flex flex-col gap-1 min-w-0 text-[13.5px]">
        <h2 class="font-semibold text-[15px]">{title}</h2>
        {children}
      </div>
    </div>
  )
}
