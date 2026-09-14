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
export interface Workload { kind: string; namespace: string; name: string; ready: number; desired: number; available: boolean; images: string; age: string; selector?: string }
export interface KService { namespace: string; name: string; type: string; clusterIP: string; externalIPs?: string[]; ports: string[]; endpoints: number; selector?: string; age: string }
export interface KIngress { namespace: string; name: string; class?: string; rules: { host: string; path: string; service: string; port: string }[]; addresses?: string[]; tlsHosts?: string[]; age: string }
export interface NetworkView { services: KService[]; ingresses: KIngress[]; pool?: { range: string; total: number; allocated: { ip: string; service: string }[] }; poolError?: string }
export interface StorageView { classes: { name: string; provisioner: string; default: boolean; reclaim: string; binding: string; expandable: boolean }[]; volumes: { name: string; capacityBytes: number; phase: string; class: string; claim?: string; accessModes: string; reclaim: string; age: string }[]; claims: { namespace: string; name: string; phase: string; requestedBytes: number; capacityBytes: number; class: string; volume?: string; age: string }[] }
export interface PodEvent { type: string; status: string; reason?: string; message?: string; since?: string }
export interface AddonStatus {
  key: string; enabled: boolean; values?: Record<string, unknown>; pinnedVersion?: string
  release?: { name: string; namespace: string; chart: string; chartVersion: string; appVersion?: string; status: string; lastDeployed?: number }
  readiness?: { namespace: string; ready: number; total: number; detail?: string[] }
  state: 'disabled' | 'pending' | 'deploying' | 'ready' | 'degraded' | 'failed' | 'orphaned'
}
export interface Settings { factoryUrl: string; discoverySubnets: string[]; watchIntervalSec: number; pxeStatusUrl: string; defaultMetalLBRange: string }
export interface PxeStatus { running: boolean; statusUrl: string; error?: string; command?: string; startedAt?: string; interface?: string; ip?: string; httpPort?: number; talosVersion?: string; schematicId?: string; boots?: { mac: string; ip?: string; arch?: string; firstSeen: string; lastSeen: string; stage: string; count: number }[]; log?: string[] }
export interface Versions { talos: string[]; talosSource: string; kubernetesMinors: string[]; kubernetesLatest: string; machinery: string; minTalos: string; note: string }

export interface InstallDisk { path?: string; selector?: { minSize?: string; type?: string; model?: string } }
export interface NodeNetwork { addresses: string[]; gateway?: string; nameservers?: string[]; vlan?: number; mtu?: number }
export interface NodeSpec { hostname: string; ip: string; mac?: string; uuid?: string; pool?: string; role?: 'controlplane' | 'worker'; arch: string; kvm?: boolean; installDisk?: InstallDisk; network?: NodeNetwork; labels?: Record<string, string>; taints?: Record<string, string>; annotations?: Record<string, string> }
export interface Pool { name: string; role: 'controlplane' | 'worker'; labels?: Record<string, string>; taints?: Record<string, string>; annotations?: Record<string, string>; extensions?: string[]; schematicID?: string; installDisk?: InstallDisk }
export interface Warning { level: 'info' | 'warn'; code: string; message: string; node?: string }
export interface AddonSpec { enabled: boolean; values?: Record<string, unknown> }
export interface PlatformSpec { metallb: AddonSpec & { range?: string }; ingressNginx: AddonSpec; gvisor: AddonSpec; metricsServer: AddonSpec; certManager: AddonSpec; argocd: AddonSpec }
export interface ClusterSpec {
  apiVersion: string; kind: string; metadata: { name: string }
  spec: {
    talosVersion: string; kubernetesVersion: string; extensions?: string[]; schematicID?: string
    controlPlane: { endpoint: string; vip?: string; allowScheduling?: boolean }
    network: { podCIDR: string; serviceCIDR: string; nameservers?: string[]; ntp?: string[] }
    pools?: Pool[]
    nodes: NodeSpec[]
    platform: PlatformSpec
  }
}
export interface ClusterRow { name: string; state: string; schematicId: string; createdAt: string; updatedAt: string; spec: ClusterSpec }

export interface Inventory {
  ip: string; hostname?: string; uuid?: string; serial?: string; cpus: number; memoryBytes: number; kvm: boolean; arch: string; talosVersion: string; platform: string; stage: string; manufacturer?: string; product?: string
  disks: { devPath: string; sizeBytes: number; model?: string; transport?: string; rotational: boolean; readonly: boolean; cdrom: boolean }[]
  links: { name: string; mac: string; up: boolean; addresses?: string[] }[]
  bootTime?: string; extensions?: { name: string; version: string; author?: string }[]
  etcd?: { memberId: string; leader: boolean; learner: boolean; dbSizeBytes: number; dbInUseBytes: number; raftIndex: number; raftTerm: number; errors?: string[] }
}
export interface Resources { cpuMilli: number; memBytes: number; pods: number }
export interface PodSummary { namespace: string; name: string; node?: string; containers?: string[]; phase: string; ready: string; restarts: number; owner?: string; cpuMilli: number; memBytes: number; age: string; usageCpuMilli?: number; usageMemBytes?: number }
export interface NodeDetail {
  name: string; ready: boolean; unschedulable: boolean; kubeletVersion: string; containerRuntime: string; kernel: string; osImage: string; internalIP: string
  conditions: { type: string; status: string; reason?: string; message?: string; since?: string }[]
  taints: string[] | null; labels: Record<string, string>; capacity: Resources; allocatable: Resources; requests: Resources; pods: PodSummary[] | null
}
export interface NodeRow { ip: string; mac: string; uuid?: string; serial?: string; ipsSeen?: string[]; cluster: string; hostname: string; pool: string; arch: string; role: string; source: string; state: string; talosVersion: string; wol: boolean; firstSeen: string; lastSeen: string; inventory?: Inventory }

export interface NodeStatus { hostname: string; ip: string; role: string; pool: string; seenAt?: string; arch: string; kvm: boolean; talosVersion: string; kubeletVersion: string; ready: boolean; unschedulable: boolean; talosReachable: boolean; talosError?: string; registered: boolean; stage: string; cpuMilli: number; cpuCapMilli: number; memBytes: number; memCapBytes: number; pods: number; podCap: number; gvisor: boolean }
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
  saveClusterForm: (name: string, form: { talosVersion: string; kubernetesVersion: string; endpoint: string; vip: string; allowScheduling: boolean | null; podCIDR: string; serviceCIDR: string; extensions: string[]; nameservers: string[]; ntp: string[] }) => req<{ yaml: string }>('PUT', `/clusters/${name}/form`, form),
  settings: () => req<Settings>('GET', '/settings'),
  machine: (mac: string) => req<NodeRow>('GET', `/machines/${mac}`),
  retireMachine: (mac: string) => req<void>('DELETE', `/machines/${mac}`),
  setWOL: (mac: string, enabled: boolean) => req<void>('PUT', `/machines/${mac}/wol`, { enabled }),
  wake: (mac: string) => req<void>('POST', `/machines/${mac}/wake`),
  design: (name: string, macs: string[], metallbRange?: string) => req<{ yaml: string; cluster: ClusterSpec; warnings: Warning[]; topology: { ControlPlanes: number; Workers: number; AllowScheduling: boolean; HA: boolean }; overlaps?: string[] }>('POST', '/config/design', { name, macs, metallbRange }),
  lint: (yaml: string) => req<{ warnings: Warning[]; yaml: string }>('POST', '/config/lint', { yaml }),
  renameNode: (cluster: string, hostname: string, to: string) => req<OpRef>('POST', `/clusters/${cluster}/nodes/${hostname}/rename`, { to }),
  moveNodePool: (cluster: string, hostname: string, pool: string) => req<OpRef>('POST', `/clusters/${cluster}/nodes/${hostname}/pool`, { pool }),
  readdressNode: (cluster: string, hostname: string, network: NodeNetwork | null, ip: string) => req<OpRef>('POST', `/clusters/${cluster}/nodes/${hostname}/readdress`, { network, ip }),
  savePools: (cluster: string, pools: Pool[]) => req<Pool[]>('PUT', `/clusters/${cluster}/pools`, pools),
  saveSettings: (v: Settings) => req<Settings>('PUT', '/settings', v),
  pxe: () => req<PxeStatus>('GET', '/pxe'),
  addons: (name: string) => req<AddonStatus[]>('GET', `/clusters/${name}/addons`),
  updateAddon: (name: string, key: string, body: { enabled?: boolean; range?: string; valuesYaml?: string }) => req<AddonStatus[]>('PUT', `/clusters/${name}/addons/${key}`, body),
  workloads: (name: string) => req<Workload[]>('GET', `/clusters/${name}/workloads`),
  pods: (name: string, namespace = '', selector = '') => req<PodSummary[]>('GET', `/clusters/${name}/pods?namespace=${encodeURIComponent(namespace)}&selector=${encodeURIComponent(selector)}`),
  podEvents: (name: string, ns: string, pod: string) => req<PodEvent[]>('GET', `/clusters/${name}/pods/${ns}/${pod}/events`),
  network: (name: string) => req<NetworkView>('GET', `/clusters/${name}/network`),
  storage: (name: string) => req<StorageView>('GET', `/clusters/${name}/storage`),
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

export function podLogsUrl(cluster: string, ns: string, pod: string, container: string, follow: boolean, tail = 500) {
  const p = new URLSearchParams({ container, tail: String(tail) })
  if (follow) p.set('follow', 'true')
  if (token) p.set('token', token)
  return `/api/v1/clusters/${cluster}/pods/${ns}/${pod}/logs?${p}`
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
      'node.rename': 'Rename node', 'node.pool': 'Move node to pool', 'node.readdress': 'Re-address node',
    } as Record<string, string>)[kind] || kind
  },
}
