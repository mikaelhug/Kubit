import { fmt, type ClusterSpec, type NodeRow, type NodeSpec, type Warning } from '../../api'
import { installCandidates } from '../../machine'

export interface Draft {
  name: string
  machines: NodeRow[]
  selected: string[]
  designedFor: string
  cluster: ClusterSpec | null
  skipPlatform: boolean
  warnings: Warning[]
}

export type SetCluster = (fn: (c: ClusterSpec) => ClusterSpec) => void
export type PatchDraft = (p: Partial<Draft>) => void

const GiB = 1024 ** 3

export function machineOf(draft: Draft, n: NodeSpec) { return draft.machines.find((m) => m.mac === n.mac) }

export function machineWarnings(m: NodeRow, all: NodeRow[]): string[] {
  const out: string[] = []
  const disks = installCandidates(m)
  if (disks.length === 0) out.push('no install disk')
  else if (disks[0].sizeBytes < 20 * GiB) out.push(`largest disk ${fmt.bytes(disks[0].sizeBytes)} (< 20 GiB)`)
  if ((m.inventory?.memoryBytes ?? 0) > 0 && (m.inventory?.memoryBytes ?? 0) < 2 * GiB) out.push(`${fmt.bytes(m.inventory!.memoryBytes)} RAM`)
  const arches = new Map<string, number>()
  for (const x of all) arches.set(x.arch, (arches.get(x.arch) ?? 0) + 1)
  if (arches.size > 1) { const majority = [...arches.entries()].sort((a, b) => b[1] - a[1])[0][0]; if (m.arch !== majority) out.push(`${m.arch} among ${majority} machines`) }
  return out
}

export function topologyText(n: number) {
  if (n === 0) return 'select at least one'
  if (n < 3) return '1 control plane (no HA), ' + (n - 1) + ' worker' + (n - 1 === 1 ? '' : 's')
  if (n < 6) return '3 schedulable control planes, ' + (n - 3) + ' worker' + (n - 3 === 1 ? '' : 's')
  return '3 dedicated control planes, ' + (n - 3) + ' workers'
}

export const registryCIDROK = (cidr: string) => { const [ip, bits] = cidr.split('/'); return /^\d+\.\d+\.\d+\.\d+$/.test(ip ?? '') && Number(bits) >= 1 && Number(bits) <= 22 }

export const hasData = (c: ClusterSpec) => !!c.spec.storage?.systemDisk || c.spec.nodes.some((n) => n.dataDisks?.length)

export const updateNode = (setCluster: SetCluster, i: number, patch: Partial<NodeSpec>) => setCluster((c) => ({ ...c, spec: { ...c.spec, nodes: c.spec.nodes.map((n, j) => j === i ? { ...n, ...patch } : n) } }))
