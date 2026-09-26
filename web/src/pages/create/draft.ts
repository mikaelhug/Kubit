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

export const platformAddons: { key: keyof ClusterSpec['spec']['platform']; title: string; what: string; size: string }[] = [
  { key: 'metallb', title: 'MetalLB', what: 'LoadBalancer addresses from the range, announced over ARP.', size: '~120 MiB, 1 controller + 1 speaker per node' },
  { key: 'ingressNginx', title: 'ingress-nginx', what: 'HTTP(S) ingress controller; the default IngressClass.', size: '~250 MiB, 1 pod' },
  { key: 'metricsServer', title: 'metrics-server', what: 'Resource metrics for kubectl top, HPA and capacity views.', size: '~100 MiB, 1 pod' },
  { key: 'certManager', title: 'cert-manager', what: 'X.509 certificates from ACME or internal CAs.', size: '~300 MiB, 3 pods' },
  { key: 'builds', title: 'Builds', what: 'Builds images from the apps repository into a private registry.', size: '~200 MiB idle, more while building' },
  { key: 'flux', title: 'Flux', what: 'GitOps sync from the Git repository below.', size: '~150 MiB, 4 pods' },
  { key: 'longhorn', title: 'Longhorn', what: 'Replicated block storage; the default StorageClass.', size: '~1 GiB, 1 manager + engine per node' },
  { key: 'gvisor', title: 'gVisor runtime class', what: 'RuntimeClass "gvisor" for sandboxed pods.', size: 'no running pods' },
]
