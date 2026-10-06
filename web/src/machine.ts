import { fmt, type Inventory, type MachineKind, type NodeRow } from './api'
import { statuses } from './store'
import { later } from './time'
import { type Tone } from './tone'

export const kindLabel: Record<MachineKind, string> = {
  member: 'cluster member',
  maintenance: 'maintenance',
  configured: 'Talos, not managed here',
  offline: 'offline',
}

export const addable = (m: NodeRow) => m.kind === 'maintenance' && !m.declared

export function kindTone(m: NodeRow): Tone {
  switch (m.kind) {
    case 'member': case 'maintenance': return 'good'
    case 'configured': return 'info'
    default: return 'muted'
  }
}

export function lastSeenOf(m: NodeRow) {
  const st = m.cluster ? statuses.value.get(m.cluster) : undefined
  const contact = st?.nodes.find((n) => n.hostname === m.hostname)?.talosReachable ? st.lastContactAt : undefined
  return contact && later(contact, m.lastSeen) ? contact : m.lastSeen
}

function isVirtual(inv?: Inventory | null) {
  if (inv?.virtual) return true
  return /qemu|kvm|vmware|virtualbox|innotek|xen|virtual machine|apple virtualization|parallels|bochs|proxmox/i.test(`${inv?.manufacturer ?? ''} ${inv?.product ?? ''}`)
}
export function typeOf(inv?: Inventory | null): 'VM' | 'metal' { return isVirtual(inv) ? 'VM' : 'metal' }

export function modelName(inv?: Inventory | null) {
  const product = inv?.product ?? ''
  const maker = inv?.manufacturer && !product.toLowerCase().startsWith(inv.manufacturer.toLowerCase()) ? inv.manufacturer : ''
  return [maker, product].filter(Boolean).join(' ').replace(/\s+/g, ' ').trim() || 'Unknown hardware'
}
export const modelOf = (m?: NodeRow | null) => modelName(m?.inventory)

export function diskCandidates(inv?: Inventory | null) {
  return (inv?.disks ?? []).filter((d) => d.devPath && !d.readonly && !d.cdrom && d.transport !== 'usb').sort((a, b) => b.sizeBytes - a.sizeBytes)
}
export const installCandidates = (m?: NodeRow | null) => diskCandidates(m?.inventory)

export function specsOf(inv?: Inventory | null) {
  if (!inv) return ''
  const disk = diskCandidates(inv)[0]
  return [`${inv.cpus} CPU`, fmt.bytes(inv.memoryBytes), disk && `${fmt.bytes(disk.sizeBytes)}${disk.transport ? ` ${disk.transport}` : ''}`].filter(Boolean).join(' · ')
}

export const roleLabel = (role?: string) => (role === 'controlplane' ? 'control plane' : 'worker')
