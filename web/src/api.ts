// Thin client for /api/v1. Every mutating call returns an operation id; progress arrives
// over the SSE stream (see events.ts).

export type Level = 'info' | 'warn' | 'error' | 'done'

export interface Event { time: string; level: Level; step: string; node?: string; message: string }
export interface Operation { id: number; cluster: string; kind: string; status: 'running' | 'done' | 'failed'; log?: string; startedAt: string; finishedAt?: string }
export interface Message { kind: 'event' | 'operation'; operationId: number; event?: Event; operation?: Operation }

export interface NodeSpec { hostname: string; ip: string; mac?: string; role: 'controlplane' | 'worker'; arch: string; kvm?: boolean; installDisk: { path?: string; selector?: { minSize?: string; type?: string; model?: string } } }
export interface ClusterSpec {
  apiVersion: string; kind: string; metadata: { name: string }
  spec: {
    talosVersion: string; kubernetesVersion: string; extensions?: string[]; schematicID?: string
    controlPlane: { endpoint: string; vip?: string; allowScheduling?: boolean }
    network: { podCIDR: string; serviceCIDR: string }
    nodes: NodeSpec[]
    platform: { metallb: { enabled: boolean; range?: string }; ingressNginx: { enabled: boolean }; gvisor: { enabled: boolean }; metricsServer: { enabled: boolean }; certManager: { enabled: boolean }; argocd: { enabled: boolean } }
  }
}
export interface ClusterRow { name: string; state: string; schematicId: string; createdAt: string; updatedAt: string; spec: ClusterSpec }

export interface Inventory { cpus: number; memoryBytes: number; kvm: boolean; arch: string; talosVersion: string; manufacturer?: string; product?: string; disks: { devPath: string; sizeBytes: number; model?: string; transport?: string; readonly: boolean; cdrom: boolean }[]; links: { name: string; mac: string; up: boolean; addresses?: string[] }[] }
export interface NodeRow { ip: string; cluster: string; hostname: string; mac: string; arch: string; role: string; source: string; state: string; talosVersion: string; lastSeen: string; inventory?: Inventory }

export interface NodeStatus { hostname: string; ip: string; role: string; arch: string; kvm: boolean; talosVersion: string; kubeletVersion: string; ready: boolean; unschedulable: boolean; talosReachable: boolean; stage: string; cpuMilli: number; cpuCapMilli: number; memBytes: number; memCapBytes: number; pods: number; podCap: number; gvisor: boolean }
export interface Status {
  name: string; state: string; talosVersion: string; kubernetesVersion: string; endpoint: string; apiReachable: boolean
  nodes: NodeStatus[]
  etcd: { members: number; expected: number; healthy: boolean; leader?: string; alarms?: string[] }
  totals: { cpuMilli: number; cpuCapMilli: number; memBytes: number; memCapBytes: number; pods: number; podCap: number; nodesReady: number; nodes: number }
  platform?: { appliedAt?: string; outputs?: Record<string, string>; error?: string }
}
export interface Service { id: string; state: string; healthy: boolean; last: string }

export class ApiError extends Error { constructor(public status: number, message: string) { super(message) } }

let token = ''
export function setToken(t: string) { token = t; try { localStorage.setItem('kubit.token', t) } catch {} }
try { token = localStorage.getItem('kubit.token') || '' } catch {}
export function getToken() { return token }

async function req<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {}
  if (token) headers.Authorization = 'Bearer ' + token
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  const res = await fetch('/api/v1' + path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) })
  if (res.status === 204) return undefined as T
  const text = await res.text()
  let data: any = text
  try { data = JSON.parse(text) } catch {}
  if (!res.ok) throw new ApiError(res.status, (data && data.error) || text || res.statusText)
  return data as T
}

export const api = {
  version: () => req<{ kubit: string }>('GET', '/version'),
  clusters: () => req<ClusterRow[]>('GET', '/clusters'),
  cluster: (name: string) => req<ClusterRow>('GET', `/clusters/${name}`),
  status: (name: string) => req<Status>('GET', `/clusters/${name}/status`),
  createCluster: (yaml: string, skipPlatform = false) => req<{ operationId: number; cluster: string }>('POST', '/clusters', { yaml, skipPlatform }),
  forgetCluster: (name: string) => req<void>('DELETE', `/clusters/${name}`),
  applyCluster: (name: string, yaml?: string) => req<{ operationId: number }>('POST', `/clusters/${name}/apply`, { yaml: yaml || '' }),
  platformPlan: (name: string) => req<{ operationId: number }>('POST', `/clusters/${name}/platform/plan`),
  platformApply: (name: string) => req<{ operationId: number }>('POST', `/clusters/${name}/platform/apply`),
  upgradeTalos: (name: string, to: string) => req<{ operationId: number }>('POST', `/clusters/${name}/upgrade/talos`, { to }),
  upgradeKubernetes: (name: string, to: string) => req<{ operationId: number }>('POST', `/clusters/${name}/upgrade/kubernetes`, { to }),
  exportCluster: (name: string, dir?: string) => req<{ dir: string }>('POST', `/clusters/${name}/export`, { dir: dir || '' }),
  addNode: (name: string, node: NodeSpec) => req<{ operationId: number }>('POST', `/clusters/${name}/nodes`, node),
  removeNode: (name: string, hostname: string, force = false) => req<{ operationId: number }>('DELETE', `/clusters/${name}/nodes/${hostname}?force=${force}`),
  nodes: (cluster?: string) => req<NodeRow[]>('GET', '/nodes' + (cluster ? `?cluster=${cluster}` : '')),
  discover: (targets: string[]) => req<{ operationId: number }>('POST', '/discover', { targets }),
  services: (ip: string) => req<Service[]>('GET', `/nodes/${ip}/services`),
  reboot: (ip: string) => req<void>('POST', `/nodes/${ip}/reboot`),
  operations: () => req<Operation[]>('GET', '/operations'),
  operation: (id: number) => req<Operation>('GET', `/operations/${id}`),
  validate: (yaml: string) => req<{ yaml: string; cluster: ClusterSpec }>('POST', '/config/validate', yaml as unknown as undefined).catch(e => { throw e }),
  draft: (name: string, ips: string[]) => req<{ yaml: string; topology: { ControlPlanes: number; Workers: number; AllowScheduling: boolean; HA: boolean } }>('POST', '/config/draft', { name, ips }),
}

// validate posts raw YAML, not JSON.
api.validate = async (yaml: string) => {
  const headers: Record<string, string> = { 'Content-Type': 'application/yaml' }
  if (token) headers.Authorization = 'Bearer ' + token
  const res = await fetch('/api/v1/config/validate', { method: 'POST', headers, body: yaml })
  const data = await res.json()
  if (!res.ok) throw new ApiError(res.status, data.error || res.statusText)
  return data
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
}
