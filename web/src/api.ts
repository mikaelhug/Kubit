// Thin client for /api/v1. Every mutating call returns an operation id; progress arrives
// over the SSE stream (see store.ts).

export type Level = 'info' | 'warn' | 'error' | 'done'
export type StepStatus = 'pending' | 'running' | 'done' | 'failed' | 'skipped' | 'cancelled'

export interface Step { id: string; title: string; status: StepStatus; node?: string; startedAt?: string; finishedAt?: string }
export interface Event { time: string; clock?: string; kind?: 'log' | 'steps' | 'step'; level: Level; step: string; node?: string; message: string; steps?: Step[]; status?: StepStatus }
export type OpStatus = 'running' | 'done' | 'failed' | 'cancelled'
export interface Operation { id: number; cluster: string; kind: string; status: OpStatus; log?: string; startedAt: string; finishedAt?: string; steps: Step[]; artifact?: unknown; request?: unknown }
export interface Message { kind: 'event' | 'operation' | 'status' | 'health'; operationId?: number; event?: Event; operation?: Operation; cluster?: string; status?: Status; health?: HealthEvent }
export interface HealthEvent { id: number; ts: string; cluster: string; node?: string; severity: 'info' | 'warn' | 'critical'; kind: string; message: string; acked: boolean }
export interface Sample { ts: string; node?: string; cpuMilli: number; cpuCap: number; memBytes: number; memCap: number; pods: number; ready: boolean; reachable: boolean }
export interface Versions { talos: string[]; talosSource: string; kubernetesMinors: string[]; kubernetesLatest: string; machinery: string; minTalos: string; note: string }

export interface NodeSpec { hostname: string; ip: string; mac?: string; role: 'controlplane' | 'worker'; arch: string; kvm?: boolean; installDisk: { path?: string; selector?: { minSize?: string; type?: string; model?: string } } }
export interface PlatformSpec { metallb: { enabled: boolean; range?: string }; ingressNginx: { enabled: boolean }; gvisor: { enabled: boolean }; metricsServer: { enabled: boolean }; certManager: { enabled: boolean }; argocd: { enabled: boolean } }
export interface ClusterSpec {
  apiVersion: string; kind: string; metadata: { name: string }
  spec: {
    talosVersion: string; kubernetesVersion: string; extensions?: string[]; schematicID?: string
    controlPlane: { endpoint: string; vip?: string; allowScheduling?: boolean }
    network: { podCIDR: string; serviceCIDR: string }
    nodes: NodeSpec[]
    platform: PlatformSpec
  }
}
export interface ClusterRow { name: string; state: string; schematicId: string; createdAt: string; updatedAt: string; spec: ClusterSpec }

export interface Inventory {
  ip: string; hostname?: string; cpus: number; memoryBytes: number; kvm: boolean; arch: string; talosVersion: string; platform: string; stage: string; manufacturer?: string; product?: string
  disks: { devPath: string; sizeBytes: number; model?: string; transport?: string; rotational: boolean; readonly: boolean; cdrom: boolean }[]
  links: { name: string; mac: string; up: boolean; addresses?: string[] }[]
  bootTime?: string; extensions?: { name: string; version: string; author?: string }[]
  etcd?: { memberId: string; leader: boolean; learner: boolean; dbSizeBytes: number; dbInUseBytes: number; raftIndex: number; raftTerm: number; errors?: string[] }
}
export interface Resources { cpuMilli: number; memBytes: number; pods: number }
export interface PodSummary { namespace: string; name: string; phase: string; ready: string; restarts: number; owner?: string; cpuMilli: number; memBytes: number; age: string; usageCpuMilli?: number; usageMemBytes?: number }
export interface NodeDetail {
  name: string; ready: boolean; unschedulable: boolean; kubeletVersion: string; containerRuntime: string; kernel: string; osImage: string; internalIP: string
  conditions: { type: string; status: string; reason?: string; message?: string; since?: string }[]
  taints: string[] | null; labels: Record<string, string>; capacity: Resources; allocatable: Resources; requests: Resources; pods: PodSummary[] | null
}
export interface NodeRow { ip: string; cluster: string; hostname: string; mac: string; arch: string; role: string; source: string; state: string; talosVersion: string; lastSeen: string; inventory?: Inventory }

export interface NodeStatus { hostname: string; ip: string; role: string; arch: string; kvm: boolean; talosVersion: string; kubeletVersion: string; ready: boolean; unschedulable: boolean; talosReachable: boolean; talosError?: string; registered: boolean; stage: string; cpuMilli: number; cpuCapMilli: number; memBytes: number; memCapBytes: number; pods: number; podCap: number; gvisor: boolean }
export interface Status {
  name: string; state: string; talosVersion: string; kubernetesVersion: string; endpoint: string; apiReachable: boolean; apiError?: string
  nodes: NodeStatus[]
  etcd: { members: number; expected: number; healthy: boolean; leader?: string; alarms?: string[] }
  totals: { cpuMilli: number; cpuCapMilli: number; memBytes: number; memCapBytes: number; pods: number; podCap: number; nodesReady: number; nodes: number }
  platform?: { appliedAt?: string; outputs?: Record<string, string>; error?: string }
}
export interface Service { id: string; state: string; healthy: boolean; last: string }

export interface AttrDiff { key: string; before?: string; after?: string; unknown?: boolean; sensitive?: boolean }
export interface PlanChange { address: string; type: string; name: string; action: 'create' | 'update' | 'replace' | 'delete'; attrs?: AttrDiff[] }
export interface PlanDiff { summary: { Add: number; Change: number; Remove: number }; groups: { addon: string; changes: PlanChange[] }[]; warnings?: string[]; timestamp: string }

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) { super(message); this.status = status }
}

let token = ''
export function setToken(t: string) { token = t; try { localStorage.setItem('kubit.token', t) } catch {} }
try { token = localStorage.getItem('kubit.token') || '' } catch {}
export function getToken() { return token }

async function req<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {}
  if (token) headers.Authorization = 'Bearer ' + token
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  const res = await fetch('/api/v1' + path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) })
  if (res.status === 204 || res.status === 202 && res.headers.get('content-length') === '0') return undefined as T
  const text = await res.text()
  let data: any = text
  try { data = JSON.parse(text) } catch {}
  if (!res.ok) throw new ApiError(res.status, (data && data.error) || text || res.statusText)
  return data as T
}

type OpRef = { operationId: number }

export const api = {
  version: () => req<{ kubit: string }>('GET', '/version'),
  clusters: () => req<ClusterRow[]>('GET', '/clusters'),
  cluster: (name: string) => req<ClusterRow>('GET', `/clusters/${name}`),
  status: (name: string, fresh = false) => req<Status>('GET', `/clusters/${name}/status${fresh ? '?fresh=true' : ''}`),
  samples: (name: string, range = '24h', node = '') => req<Sample[]>('GET', `/clusters/${name}/samples?range=${range}&node=${encodeURIComponent(node)}`),
  events: (name: string, unacked = false) => req<HealthEvent[]>('GET', `/clusters/${name}/events?limit=200&unacked=${unacked}`),
  ackEvent: (id: number) => req<void>('POST', `/events/${id}/ack`),
  ackAll: (name: string) => req<void>('POST', `/clusters/${name}/events/ack`),
  versions: () => req<Versions>('GET', '/versions'),
  clusterYaml: (name: string) => req<string>('GET', `/clusters/${name}/yaml`),
  saveClusterYaml: async (name: string, yaml: string): Promise<{ yaml: string }> => {
    const headers: Record<string, string> = { 'Content-Type': 'application/yaml' }
    if (token) headers.Authorization = 'Bearer ' + token
    const res = await fetch(`/api/v1/clusters/${name}/yaml`, { method: 'PUT', headers, body: yaml })
    const data = await res.json()
    if (!res.ok) throw new ApiError(res.status, data.error || res.statusText)
    return data
  },
  createCluster: (yaml: string, skipPlatform = false) => req<OpRef & { cluster: string }>('POST', '/clusters', { yaml, skipPlatform }),
  forgetCluster: (name: string) => req<void>('DELETE', `/clusters/${name}`),
  applyCluster: (name: string, yaml?: string) => req<OpRef>('POST', `/clusters/${name}/apply`, { yaml: yaml || '' }),
  platformPlan: (name: string) => req<OpRef>('POST', `/clusters/${name}/platform/plan`),
  platformApply: (name: string) => req<OpRef>('POST', `/clusters/${name}/platform/apply`),
  platformApplyPlan: (name: string, planId: number) => req<OpRef>('POST', `/clusters/${name}/platform/apply/${planId}`),
  upgradeTalos: (name: string, to: string) => req<OpRef>('POST', `/clusters/${name}/upgrade/talos`, { to }),
  upgradeKubernetes: (name: string, to: string) => req<OpRef>('POST', `/clusters/${name}/upgrade/kubernetes`, { to }),
  exportCluster: (name: string, dir?: string) => req<{ dir: string }>('POST', `/clusters/${name}/export`, { dir: dir || '' }),
  addNode: (name: string, node: NodeSpec) => req<OpRef>('POST', `/clusters/${name}/nodes`, node),
  removeNode: (name: string, hostname: string, force = false) => req<OpRef>('DELETE', `/clusters/${name}/nodes/${hostname}?force=${force}`),
  nodes: (cluster?: string) => req<NodeRow[]>('GET', '/nodes' + (cluster ? `?cluster=${cluster}` : '')),
  discover: (targets: string[]) => req<OpRef>('POST', '/discover', { targets }),
  services: (ip: string) => req<Service[]>('GET', `/nodes/${ip}/services`),
  inventory: (ip: string) => req<Inventory>('GET', `/nodes/${ip}/inventory`),
  nodeKubernetes: (ip: string) => req<NodeDetail>('GET', `/nodes/${ip}/kubernetes`),
  cordon: (cluster: string, hostname: string) => req<OpRef>('POST', `/clusters/${cluster}/nodes/${hostname}/cordon`),
  uncordon: (cluster: string, hostname: string) => req<OpRef>('POST', `/clusters/${cluster}/nodes/${hostname}/uncordon`),
  drain: (cluster: string, hostname: string) => req<OpRef>('POST', `/clusters/${cluster}/nodes/${hostname}/drain`),
  rebootNode: (cluster: string, hostname: string, drain: boolean) => req<OpRef>('POST', `/clusters/${cluster}/nodes/${hostname}/reboot`, { drain }),
  upgradeNode: (cluster: string, hostname: string, to: string) => req<OpRef>('POST', `/clusters/${cluster}/nodes/${hostname}/upgrade`, { to }),
  reboot: (ip: string) => req<void>('POST', `/nodes/${ip}/reboot`),
  operations: () => req<Operation[]>('GET', '/operations'),
  operation: (id: number) => req<Operation>('GET', `/operations/${id}`),
  cancelOperation: (id: number) => req<void>('DELETE', `/operations/${id}`),
  retryOperation: (id: number) => req<OpRef>('POST', `/operations/${id}/retry`),
  validate: async (yaml: string): Promise<{ yaml: string; cluster: ClusterSpec }> => {
    // Posts raw YAML, not JSON.
    const headers: Record<string, string> = { 'Content-Type': 'application/yaml' }
    if (token) headers.Authorization = 'Bearer ' + token
    const res = await fetch('/api/v1/config/validate', { method: 'POST', headers, body: yaml })
    const data = await res.json()
    if (!res.ok) throw new ApiError(res.status, data.error || res.statusText)
    return data
  },
  draft: (name: string, ips: string[]) => req<{ yaml: string; topology: { ControlPlanes: number; Workers: number; AllowScheduling: boolean; HA: boolean } }>('POST', '/config/draft', { name, ips }),
}

export function logsUrl(ip: string, service?: string, follow = false) {
  const p = new URLSearchParams()
  if (service) p.set('service', service)
  if (follow) p.set('follow', 'true')
  if (token) p.set('token', token)
  return `/api/v1/nodes/${ip}/logs?${p}`
}

export const fmt = {
  bytes(b: number) {
    if (!b) return '0'
    const u = ['B', 'K', 'M', 'G', 'T']
    let i = 0
    let v = b
    while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
    return (i >= 3 && v < 100 ? v.toFixed(1) : Math.round(v)) + u[i]
  },
  cores(m: number) { return m >= 1000 ? (m / 1000).toFixed(1) : `${m}m` },
  pct(a: number, b: number) { return b ? Math.round((a / b) * 100) : 0 },
  time(iso: string) { return iso ? new Date(iso).toLocaleTimeString() : '' },
  datetime(iso: string) { return iso ? new Date(iso).toLocaleString() : '' },
  /** Time of day when today, otherwise date and time. */
  when(iso: string) {
    if (!iso) return ''
    const d = new Date(iso)
    return d.toDateString() === new Date().toDateString() ? d.toLocaleTimeString() : d.toLocaleString()
  },
  duration(from?: string, to?: string) {
    if (!from) return ''
    const ms = (to ? new Date(to).getTime() : Date.now()) - new Date(from).getTime()
    if (ms < 1000) return '<1s'
    const s = Math.round(ms / 1000)
    if (s < 60) return `${s}s`
    const m = Math.floor(s / 60)
    return `${m}m ${s % 60}s`
  },
  kind(kind: string) {
    return ({
      'cluster.create': 'Create cluster', 'cluster.apply': 'Apply cluster.yaml', 'platform.plan': 'Plan add-ons', 'platform.apply': 'Apply add-ons',
      'upgrade.talos': 'Upgrade Talos', 'upgrade.kubernetes': 'Upgrade Kubernetes', 'node.add': 'Add node', 'node.remove': 'Remove node', discover: 'Discover nodes',
      'node.cordon': 'Cordon node', 'node.uncordon': 'Uncordon node', 'node.drain': 'Drain node', 'node.reboot': 'Reboot node', 'node.upgrade': 'Upgrade node',
    } as Record<string, string>)[kind] || kind
  },
}
