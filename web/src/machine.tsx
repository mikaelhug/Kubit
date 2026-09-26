import type { LabHost, MachineKind, NodeRow } from './api'
import { machines, statuses } from './store'
import { Pill, stateTone, type Tone } from './components/ui'

export const kindLabel: Record<MachineKind, string> = {
  member: 'cluster member',
  maintenance: 'Talos maintenance',
  configured: 'Talos, not managed here',
  labhost: 'lab host',
  booting: 'booting',
  unbooted: 'not running Talos',
}

export function kindTone(m: NodeRow): Tone {
  switch (m.kind) {
    case 'member': case 'maintenance': return 'good'
    case 'labhost': return stateTone(labState(m.labhost) || 'labhost')
    case 'booting': return 'warn'
    case 'configured': return 'info'
    default: return stateTone(m.state)
  }
}

export function isLabVM(m?: NodeRow | null) { return !!m?.host }
export function lastSeenOf(m: NodeRow) {
  if (m.labhost?.metrics?.at) return m.labhost.metrics.at
  const st = m.cluster ? statuses.value.get(m.cluster) : undefined
  const contact = st?.nodes.find((n) => n.hostname === m.hostname)?.talosReachable ? st.lastContactAt : undefined
  return contact && contact > m.lastSeen ? contact : m.lastSeen
}
export function onMac(lh?: LabHost | null) { return lh?.driver === 'vfkit' }
export function hostOf(m?: NodeRow | null) { return m?.host ? machines.value.get(m.host.toLowerCase()) : undefined }
export function hostName(m?: NodeRow | null) { return m ? m.labhost?.capacity.hostname || m.hostname || m.mac : '' }

export type MachineGroup = 'available' | 'boot' | 'in-use'
export const groupLabel: Record<MachineGroup, string> = { available: 'Available', boot: 'Needs boot', 'in-use': 'In use' }
export function groupOf(m: NodeRow): MachineGroup {
  switch (m.kind) {
    case 'maintenance': return 'available'
    case 'member': case 'labhost': return 'in-use'
    default: return 'boot'
  }
}

export function canAdopt(m: NodeRow) { return m.kind === 'maintenance' }
export function canMakeLabHost(m: NodeRow) { return !m.host && !m.cluster && (!m.labhost || (m.labhost.state === 'error' && !onMac(m.labhost))) }
export function canRetire(m: NodeRow) { return m.kind !== 'labhost' && !(m.host && hostOf(m)) }
export function bootTalosBlocked(m: NodeRow): string {
  if (m.cluster) return 'Members are removed from the cluster first; that resets them to maintenance mode from disk'
  if (m.kind === 'labhost') return 'A lab host boots its own disk; release it first'
  if (m.host) return 'Lab VMs are re-provisioned from their host'
  return ''
}
export function provisionLabel(m: NodeRow) { return m.provisionKind === 'labhost' ? 'boot→Debian' : 'boot→Talos' }

export function isVirtual(m?: NodeRow | null) {
  const inv = m?.inventory
  if (inv?.virtual) return true
  return /qemu|kvm|vmware|virtualbox|innotek|xen|virtual machine|apple virtualization|parallels|bochs|proxmox/i.test(`${inv?.manufacturer ?? ''} ${inv?.product ?? ''}`)
}
export function formOf(m?: NodeRow | null): 'lab host' | 'lab VM' | 'VM' | 'metal' {
  if (m?.labhost) return 'lab host'
  if (m?.host) return 'lab VM'
  return isVirtual(m) ? 'VM' : 'metal'
}
export function modelOf(m?: NodeRow | null) {
  const inv = m?.inventory
  const name = [inv?.manufacturer, inv?.product].filter(Boolean).join(' ').replace(/\s+/g, ' ').trim()
  return name || 'Unknown hardware'
}

export function installCandidates(m?: NodeRow | null) {
  const disks = (m?.inventory?.disks ?? []).filter((d) => d.devPath && !d.readonly && !d.cdrom && d.transport !== 'usb').sort((a, b) => b.sizeBytes - a.sizeBytes)
  return m?.host ? disks.sort((a, b) => (a.devPath === '/dev/vda' ? -1 : b.devPath === '/dev/vda' ? 1 : 0)) : disks
}
export function dataCandidates(m: NodeRow | undefined | null, install?: string) { return installCandidates(m).filter((d) => d.devPath !== install) }

export function labState(lh?: LabHost | null) { return lh ? lh.state === 'ready' && (lh.failures ?? 0) >= 3 ? 'offline' : lh.state : '' }
export function labOffline(lh?: LabHost | null) { return labState(lh) === 'offline' }

export function kindDetail(m: NodeRow) { return m.kind === 'labhost' ? labState(m.labhost) : m.kind === 'unbooted' && m.state !== 'unknown' ? m.state : '' }

export function KindPill({ m }: { m: NodeRow }) {
  const detail = kindDetail(m)
  return <Pill tone={kindTone(m)} title={`state ${m.state}`}>{kindLabel[m.kind]}{detail && detail !== m.kind ? ` · ${detail}` : ''}</Pill>
}

export function TypePill({ m }: { m?: NodeRow | null }) {
  const form = formOf(m)
  const title = { 'lab host': onMac(m?.labhost) ? 'This Mac, running Talos VMs' : 'KVM host Kubit installed', 'lab VM': `Talos VM on lab host ${hostName(hostOf(m)) || m?.host}`, VM: "Virtual machine: shares its host's failure domain", metal: 'Bare metal' }[form]
  return <Pill tone={form === 'metal' ? 'muted' : 'info'} title={title}>{form}</Pill>
}
