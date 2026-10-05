import { type MachineKind, type NodeRow } from './api'
import { statuses } from './store'
import { later } from './time'
import { stateTone, type Tone } from './tone'

export const kindLabel: Record<MachineKind, string> = {
  member: 'cluster member',
  maintenance: 'maintenance',
  configured: 'Talos, not managed here',
  booting: 'booting',
  unbooted: 'not running Talos',
}

export function kindTone(m: NodeRow): Tone {
  switch (m.kind) {
    case 'member': case 'maintenance': return 'good'
    case 'booting': return 'warn'
    case 'configured': return 'info'
    default: return stateTone(m.state)
  }
}

export function lastSeenOf(m: NodeRow) {
  const st = m.cluster ? statuses.value.get(m.cluster) : undefined
  const contact = st?.nodes.find((n) => n.hostname === m.hostname)?.talosReachable ? st.lastContactAt : undefined
  return contact && later(contact, m.lastSeen) ? contact : m.lastSeen
}

function isVirtual(m?: NodeRow | null) {
  const inv = m?.inventory
  if (inv?.virtual) return true
  return /qemu|kvm|vmware|virtualbox|innotek|xen|virtual machine|apple virtualization|parallels|bochs|proxmox/i.test(`${inv?.manufacturer ?? ''} ${inv?.product ?? ''}`)
}
export function typeOf(m?: NodeRow | null): 'VM' | 'metal' { return isVirtual(m) ? 'VM' : 'metal' }
export function modelOf(m?: NodeRow | null) {
  const inv = m?.inventory
  const name = [inv?.manufacturer, inv?.product].filter(Boolean).join(' ').replace(/\s+/g, ' ').trim()
  return name || 'Unknown hardware'
}

export function installCandidates(m?: NodeRow | null) {
  return (m?.inventory?.disks ?? []).filter((d) => d.devPath && !d.readonly && !d.cdrom && d.transport !== 'usb').sort((a, b) => b.sizeBytes - a.sizeBytes)
}

export function kindDetail(m: NodeRow) { return m.kind === 'unbooted' && m.state !== 'unknown' ? m.state : '' }

export function nodeEntry(m: NodeRow, hostname = '') {
  const inv = m.inventory
  const disk = installCandidates(m).find((d) => !d.rotational) ?? installCandidates(m)[0]
  const lines = [
    `- hostname: ${hostname || inv?.hostname || `node-${m.mac.replaceAll(':', '').slice(-4)}`}`,
    `  ip: ${m.ip}`,
    `  mac: "${m.mac}"`,
    `  role: worker`,
    `  arch: ${inv?.arch || m.arch || 'amd64'}`,
  ]
  if (disk) lines.push(`  installDisk: { path: ${disk.devPath} }`)
  if (inv?.kvm) lines.push('  kvm: true')
  if (inv?.tpm) lines.push('  tpm: true')
  if (inv?.watchdog) lines.push('  watchdog: true')
  return lines.join('\n') + '\n'
}
