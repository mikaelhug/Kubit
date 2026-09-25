// Thin client for /api/v1. Every mutating call returns an operation id; progress arrives
// over the SSE stream (see store.ts).

export type Level = 'info' | 'warn' | 'error' | 'done'
export type StepStatus = 'pending' | 'running' | 'done' | 'failed' | 'skipped' | 'cancelled'

export interface Step { id: string; title: string; status: StepStatus; node?: string; startedAt?: string; finishedAt?: string }
export interface Event { time: string; clock?: string; kind?: 'log' | 'steps' | 'step'; level: Level; step: string; node?: string; message: string; steps?: Step[]; status?: StepStatus }
export type OpStatus = 'running' | 'done' | 'failed' | 'cancelled'
export interface Operation { id: number; cluster: string; kind: string; status: OpStatus; log?: string; startedAt: string; finishedAt?: string; steps: Step[]; artifact?: unknown; request?: unknown }
export interface Message { seq?: number; kind: 'hello' | 'resync' | 'event' | 'operation' | 'status' | 'health' | 'refresh' | 'cluster' | 'clusterRemoved' | 'machine' | 'machineRemoved' | 'snapshot' | 'snapshotRemoved' | 'audit' | 'settings' | 'healthAck' | 'healthResolved' | 'versions' | 'hostSample' | 'observer'; observer?: ObserverState; operationId?: number; event?: Event; operation?: Operation; cluster?: string; status?: Status; health?: HealthEvent; scope?: string; clusterRow?: ClusterRow; machine?: NodeRow; snapshot?: Snapshot; audit?: AuditEntry; settings?: Settings; sample?: Sample; key?: string; node?: string; hello?: { seq: number; version: string; startedAt: string; service: boolean; pid: number; os?: string } }
/** Kubit's own ability to observe: network reach and observation gaps (host asleep). */
export interface ObserverState { online: boolean; since?: string; error?: string; gaps24h: number; lastGapAt?: string }
export const kubitKey = 'kubit'
export interface HealthEvent { id: number; ts: string; cluster: string; node?: string; severity: 'info' | 'warn' | 'critical'; kind: string; message: string; acked: boolean }
export interface ServiceHealth { collectedAt: string; metallb: boolean; workloads?: { kind: string; namespace: string; name: string; ready: number; desired: number; available: boolean; ageSec: number }[]; pods?: { namespace: string; name: string; node?: string; owner?: string; phase: string; restarts: number; ageSec: number }[]; claims?: { namespace: string; name: string; phase: string; ageSec: number }[]; services?: { namespace: string; name: string; type: string; hasSelector: boolean; endpoints: number; ageSec: number }[]; ingresses?: { namespace: string; name: string; hasAddress: boolean; ageSec: number }[]; pool?: { range: string; total: number; allocated: number } }
export interface Sample { ts: string; node?: string; cpuMilli: number; cpuCap: number; memBytes: number; memCap: number; pods: number; ready: boolean; reachable: boolean; disk?: number; diskCap?: number }
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
export interface AlertSettings { minSeverity: 'info' | 'warn' | 'critical'; webhookUrl: string; smtp: { host: string; port: number; from: string; to: string[]; username: string; password: string; startTLS: boolean; tls?: 'starttls' | 'tls' | 'none' }; ignoreNamespaces: string[]; heartbeatHours: number }
export interface OffsiteTarget { type: '' | 'dir' | 's3'; prefix: string; dir: string; endpoint: string; bucket: string; region: string; accessKey: string; secretKey: string; insecure: boolean; pathStyle: boolean; keepBackups: number }
export interface OffsiteStatus { target: string; enabled: boolean; lastBackup?: string; backups: number; snapshots: number; bytes: number; error?: string }
export interface Settings { factoryUrl: string; discoverySubnets: string[]; watchIntervalSec: number; pxeStatusUrl: string; defaultMetalLBRange: string; alerts: AlertSettings; offsite: OffsiteTarget; pxeEnrollment: 'open' | 'closed'; amt: OOBConfig; bmc: OOBConfig; auth: { oidc: OIDCSettings } }
export interface PxeStatus { running: boolean; statusUrl: string; error?: string; command?: string; serviceCommand?: string; startedAt?: string; interface?: string; httpOnly?: boolean; ip?: string; httpPort?: number; talosVersion?: string; schematicId?: string; boots?: { mac: string; ip?: string; arch?: string; firstSeen: string; lastSeen: string; stage: string; count: number }[]; log?: string[] }
export interface Versions { talos: string[]; talosSource: string; kubernetesMinors: string[]; kubernetesLatest: string; machinery: string; minTalos: string; note: string }

export interface InstallDisk { path?: string; selector?: { minSize?: string; type?: string; model?: string } }
export interface NodeNetwork { addresses: string[]; gateway?: string; nameservers?: string[]; vlan?: number; mtu?: number }
export interface NodeSpec { hostname: string; ip: string; mac?: string; uuid?: string; pool?: string; role?: 'controlplane' | 'worker'; arch: string; kvm?: boolean; installDisk?: InstallDisk; dataDisks?: string[]; network?: NodeNetwork; labels?: Record<string, string>; taints?: Record<string, string>; annotations?: Record<string, string> }
export interface Pool { name: string; role: 'controlplane' | 'worker'; labels?: Record<string, string>; taints?: Record<string, string>; annotations?: Record<string, string>; extensions?: string[]; schematicID?: string; installDisk?: InstallDisk }
export interface Warning { level: 'info' | 'warn'; code: string; message: string; node?: string }
export interface AddonSpec { enabled: boolean; values?: Record<string, unknown> }
export interface Snapshot { id: number; cluster: string; ts: string; node: string; sizeBytes: number; sha256: string; keys: number; talosVersion?: string; k8sVersion?: string; source: 'manual' | 'schedule' | 'pre-upgrade'; status: 'ok' | 'corrupt' | 'missing'; offsite?: string }
export interface PlatformSpec { metallb: AddonSpec & { range?: string }; ingressNginx: AddonSpec; gvisor: AddonSpec; metricsServer: AddonSpec; certManager: AddonSpec; argocd: AddonSpec; longhorn: AddonSpec }
export interface ClusterSpec {
  apiVersion: string; kind: string; metadata: { name: string }
  spec: {
    talosVersion: string; kubernetesVersion: string; extensions?: string[]; schematicID?: string
    controlPlane: { endpoint: string; vip?: string; allowScheduling?: boolean }
    network: { podCIDR: string; serviceCIDR: string; nameservers?: string[]; ntp?: string[] }
    pools?: Pool[]
    nodes: NodeSpec[]
    platform: PlatformSpec
    backup?: { etcd: { interval?: string; keep?: number } }
    maintenance?: { window?: string; timezone?: string }
    auth?: { oidc?: ClusterOIDC }
  }
}
export interface ClusterOIDC { issuer: string; clientID: string; usernameClaim?: string; usernamePrefix?: string; groupsClaim?: string; groupsPrefix?: string; adminGroup?: string }
export interface ClusterForm { oidc?: ClusterOIDC | null; talosVersion: string; kubernetesVersion: string; endpoint: string; vip: string; allowScheduling: boolean | null; podCIDR: string; serviceCIDR: string; extensions: string[]; nameservers: string[]; ntp: string[]; etcdSnapshotInterval: string; etcdSnapshotKeep: number; maintenanceWindow: string; maintenanceTimezone: string }
/** The structured-settings form as the daemon currently stores it. */
export function formOf(spec: ClusterSpec['spec']): ClusterForm {
  return {
    talosVersion: spec.talosVersion, kubernetesVersion: spec.kubernetesVersion, endpoint: spec.controlPlane.endpoint, vip: spec.controlPlane.vip ?? '', allowScheduling: spec.controlPlane.allowScheduling ?? null,
    podCIDR: spec.network.podCIDR, serviceCIDR: spec.network.serviceCIDR, extensions: spec.extensions ?? [], nameservers: spec.network.nameservers ?? [], ntp: spec.network.ntp ?? [],
    etcdSnapshotInterval: spec.backup?.etcd.interval ?? '6h', etcdSnapshotKeep: spec.backup?.etcd.keep ?? 28, maintenanceWindow: spec.maintenance?.window ?? '', maintenanceTimezone: spec.maintenance?.timezone ?? '', oidc: spec.auth?.oidc ?? null,
  }
}
export interface CertInfo { name: string; subject: string; issuer?: string; notBefore: string; notAfter: string; daysLeft: number; rotatable: boolean; error?: string }
export interface AuditEntry { id: number; at: string; cluster: string; action: string; detail: string; actor?: string }
export interface MaintenanceState { window: string; timezone: string; open: boolean; next?: string }
export interface ClusterRow { name: string; state: string; schematicId: string; createdAt: string; updatedAt: string; spec: ClusterSpec }

export interface Inventory {
  ip: string; hostname?: string; uuid?: string; serial?: string; cpus: number; memoryBytes: number; kvm: boolean; virtual?: boolean; arch: string; talosVersion: string; platform: string; stage: string; manufacturer?: string; product?: string
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
export interface OOBConfig { type: '' | 'amt' | 'redfish'; host: string; user: string; password: string; tls: boolean }
export const oobLabel = (t?: string) => t === 'amt' ? 'Intel AMT' : t === 'redfish' ? 'BMC (Redfish)' : 'remote management'
export interface OOBInfo { version: string; mac: string; uuid?: string; manufacturer?: string; model?: string; serial?: string; power: string; cpus?: number; memoryBytes?: number; disks?: { model?: string; sizeBytes: number; transport?: string; media?: string }[] }
export interface LabVM { name: string; mac: string; state: string; cpus: number; memMiB: number; diskGiB: number; dataGiB?: number; boot: 'talos' | 'disk'; ip?: string }
export interface LabCapacity { cpus: number; memMiB: number; diskGiB: number; kvm: boolean; kernel: string; libvirt: string; hostname: string; arch: string; bridge: string; ready: boolean; checkedAt: string; model?: string; os?: string; hypervisor?: string; reserveMiB?: number; problem?: string; command?: string }
export interface LabLocal { supported: boolean; problem?: string; command?: string; capacity?: LabCapacity; subnet?: string; host?: string }
export interface LabMetrics { load1: number; cpuPct: number; memUsed: number; memTotal: number; diskUsed: number; diskTotal: number; vmsRunning: number; uptimeSec: number; at: string }
export interface LabUpdates { count: number; security: number; rebootRequired: boolean; kernelRunning: string; kernelInstalled: string; release: string; unattended: boolean; checkedAt: string }
export interface VMSize { name?: string; role: 'controlplane' | 'worker'; cpus: number; memMiB: number; diskGiB: number; dataGiB: number }
export interface VMPlan { each: VMSize[]; prefix?: string }
export interface LabBootLine { kernel: string; initrd: string; cmdline: string }
export interface LabInstall { stage: 'installer' | 'partitioning' | 'packages' | 'late-done' | 'booted' | string; at: string }
export interface LabHost { state: 'installing' | 'setup' | 'ready' | 'updating' | 'error'; error?: string; capacity: LabCapacity; talos?: string; /** null from the daemon while installing; readers use vmsOf */ vms: LabVM[] | null; metrics?: LabMetrics; updates?: LabUpdates; install?: LabInstall; network?: 'bridge' | 'routed'; disk?: string; boot?: LabBootLine; failures?: number; driver?: 'libvirt' | 'vfkit'; iso?: string; updatedAt: string }
export function vmsOf(lh?: LabHost | null): LabVM[] { return lh?.vms ?? [] }
/** Samples and events of a lab host are filed under this pseudo-cluster. */
export function labHostKey(mac: string) { return `labhost:${mac.toLowerCase()}` }

/** A newer installed kernel or the reboot-required flag: the next Update host will reboot. */
export function labNeedsReboot(u?: LabUpdates) { return !!u && (u.rebootRequired || (!!u.kernelInstalled && !!u.kernelRunning && u.kernelInstalled !== u.kernelRunning)) }
export type MachineKind = 'member' | 'maintenance' | 'configured' | 'labhost' | 'booting' | 'unbooted'
export interface NodeRow { ip: string; mac: string; uuid?: string; serial?: string; ipsSeen?: string[]; cluster: string; hostname: string; pool: string; arch: string; role: string; source: string; state: string; kind: MachineKind; talos: boolean; talosVersion: string; wol: boolean; oob?: OOBConfig; oobType?: string; provision?: boolean; provisionKind?: string; labhost?: LabHost; host?: string; firstSeen: string; lastSeen: string; inventory?: Inventory }

export interface NodeStatus { hostname: string; ip: string; role: string; pool: string; seenAt?: string; arch: string; kvm: boolean; talosVersion: string; kubeletVersion: string; ready: boolean; unschedulable: boolean; talosReachable: boolean; talosError?: string; talosReach?: string; registered: boolean; stage: string; cpuMilli: number; cpuCapMilli: number; memBytes: number; memCapBytes: number; memAllocBytes: number; pods: number; podCap: number; gvisor: boolean }
export interface Status {
  name: string; state: string; talosVersion: string; kubernetesVersion: string; endpoint: string; apiReachable: boolean; apiError?: string; apiReach?: string; health?: 'healthy' | 'degraded' | 'down' | 'unknown'; openAlerts?: number
  observer?: 'online' | 'offline'; observerError?: string; lastContactAt?: string
  nodes: NodeStatus[]
  etcd: { members: number; expected: number; healthy: boolean; leader?: string; alarms?: string[] }
  totals: { cpuMilli: number; cpuCapMilli: number; memBytes: number; memCapBytes: number; pods: number; podCap: number; nodesReady: number; nodes: number }
  platform?: { appliedAt?: string; outputs?: Record<string, string>; error?: string }
  observedAt?: string; lastSnapshotAt?: string; snapshotInterval?: string
}
export interface Service { id: string; state: string; healthy: boolean; last: string }

export interface AttrDiff { key: string; before?: string; after?: string; unknown?: boolean; sensitive?: boolean }
export interface PlanChange { address: string; type: string; name: string; action: 'create' | 'update' | 'replace' | 'delete'; attrs?: AttrDiff[] }
export interface PlanDiff { summary: { Add: number; Change: number; Remove: number }; groups: { addon: string; changes: PlanChange[] }[]; warnings?: string[]; timestamp: string }

export class ApiError extends Error {
  status: number
  code?: string
  command?: string
  body?: any
  constructor(status: number, message: string, extra?: { code?: string; command?: string; body?: any }) { super(message); this.status = status; this.code = extra?.code; this.command = extra?.command; this.body = extra?.body }
}

let token = ''
export function setToken(t: string) { token = t; try { localStorage.setItem('kubit.token', t) } catch {} }
try { token = localStorage.getItem('kubit.token') || '' } catch {}
export function getToken() { return token }

/** Called once per 401 so the shell can switch to the sign-in screen. */
export let onUnauthorized: () => void = () => {}
export function setUnauthorizedHandler(fn: () => void) { onUnauthorized = fn }

async function req<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {}
  if (token) headers.Authorization = 'Bearer ' + token
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  const res = await fetch('/api/v1' + path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) })
  if (res.status === 204 || res.status === 202 && res.headers.get('content-length') === '0') return undefined as T
  const text = await res.text()
  let data: any = text
  try { data = JSON.parse(text) } catch {}
  if (res.status === 401 && !path.startsWith('/auth/')) onUnauthorized()
  if (!res.ok) throw new ApiError(res.status, (data && data.error) || text || res.statusText, data && typeof data === 'object' ? { code: data.code, command: data.command, body: data } : undefined)
  return data as T
}

export type Role = 'viewer' | 'operator' | 'admin'
export interface Me { user: string; role: Role; via: string; setup: boolean; users: number; sso?: string }
export interface OIDCSettings { enabled: boolean; name: string; issuer: string; clientId: string; clientSecret: string; usernameClaim: string; groupsClaim: string; adminGroups: string[]; operatorGroups: string[]; viewerGroups: string[]; defaultRole: '' | Role }
export interface User { id: number; name: string; role: Role; disabled: boolean; source: string; createdAt: string; lastLogin?: string; hasPassword: boolean }
export interface ApiToken { name: string; kind: string; createdAt: string; expiresAt?: string; lastUsed?: string; prefix: string }
export const roleRank: Record<Role, number> = { viewer: 1, operator: 2, admin: 3 }

type OpRef = { operationId: number }

export const api = {
  me: () => req<Me>('GET', '/auth/me'),
  login: (name: string, password: string) => req<Me>('POST', '/auth/login', { name, password }),
  setup: (name: string, password: string) => req<Me>('POST', '/auth/setup', { name, password }),
  logout: () => req<void>('POST', '/auth/logout'),
  users: () => req<User[]>('GET', '/users'),
  createUser: (name: string, password: string, role: Role) => req<User>('POST', '/users', { name, password, role }),
  updateUser: (name: string, patch: { role?: Role; disabled?: boolean; password?: string }) => req<User>('PUT', `/users/${name}`, patch),
  deleteUser: (name: string) => req<void>('DELETE', `/users/${name}`),
  tokens: (user: string) => req<ApiToken[]>('GET', `/users/${user}/tokens`),
  createToken: (user: string, name: string, days: number) => req<{ token: string }>('POST', `/users/${user}/tokens`, { name, days }),
  deleteToken: (user: string, name: string) => req<void>('DELETE', `/users/${user}/tokens/${encodeURIComponent(name)}`),
  version: () => req<{ kubit: string; startedAt?: string; service?: boolean; pid?: number }>('GET', '/version'),
  observer: () => req<ObserverState>('GET', '/observer'),
  clusters: () => req<ClusterRow[]>('GET', '/clusters'),
  cluster: (name: string) => req<ClusterRow>('GET', `/clusters/${name}`),
  status: (name: string, fresh = false) => req<Status>('GET', `/clusters/${name}/status${fresh ? '?fresh=true' : ''}`),
  samples: (name: string, range = '24h', node = '') => req<Sample[]>('GET', `/clusters/${name}/samples?range=${range}&node=${encodeURIComponent(node)}`),
  events: (name: string, unacked = false) => req<HealthEvent[]>('GET', `/clusters/${name}/events?limit=200&unacked=${unacked}`),
  ackEvent: (id: number) => req<void>('POST', `/events/${id}/ack`),
  ackAll: (name: string) => req<void>('POST', `/clusters/${name}/events/ack`),
  versions: () => req<Versions>('GET', '/versions'),
  saveClusterForm: (name: string, form: ClusterForm) => req<{ yaml: string }>('PUT', `/clusters/${name}/form`, form),
  settings: () => req<Settings>('GET', '/settings'),
  serviceHealth: (name: string) => req<{ latest: ServiceHealth | null; alerts: HealthEvent[] }>('GET', `/clusters/${name}/service-health`),
  machine: (mac: string) => req<NodeRow>('GET', `/machines/${mac}`),
  retireMachine: (mac: string) => req<void>('DELETE', `/machines/${mac}`),
  setWOL: (mac: string, enabled: boolean) => req<void>('PUT', `/machines/${mac}/wol`, { enabled }),
  wake: (mac: string) => req<void>('POST', `/machines/${mac}/wake`),
  saveOOB: (mac: string, c: OOBConfig) => req<void>('PUT', `/machines/${mac}/oob`, c),
  oobTest: (mac: string, c?: OOBConfig) => req<{ ok: boolean; error?: string; info?: OOBInfo }>('POST', `/machines/${mac}/oob/test`, c ?? {}),
  power: (mac: string, action: 'on' | 'off' | 'reset' | 'cycle' | 'pxe') => req<OpRef>('POST', `/machines/${mac}/power`, { action }),
  addOOBMachine: (c: OOBConfig) => req<{ machine: NodeRow; info: OOBInfo }>('POST', '/machines/oob', c),
  labProvision: (mac: string, plan?: { manual?: boolean; network?: 'bridge' | 'routed'; disk?: string; vms?: VMPlan; cluster?: { name: string; controlPlanes: 1 | 3; skipPlatform?: boolean } }) => req<OpRef>('POST', `/machines/${mac}/labhost`, plan ?? {}),
  labRelease: (mac: string) => req<void>('DELETE', `/machines/${mac}/labhost`),
  labLocal: () => req<LabLocal>('GET', '/labhosts/local'),
  labLocalCreate: (plan: { vms?: VMPlan; cluster?: { name: string; controlPlanes: 1 | 3; skipPlatform?: boolean } }) => req<OpRef & { mac: string }>('POST', '/labhosts', { driver: 'vfkit', ...plan }),
  addMachine: (r: { mac: string; ip?: string; hostname?: string; arch?: string }) => req<NodeRow>('POST', '/machines', r),
  labSamples: (mac: string, range: string) => req<Sample[]>('GET', `/machines/${mac}/labhost/samples?range=${range}`),
  labCheck: (mac: string) => req<LabUpdates>('POST', `/machines/${mac}/labhost/check`),
  labUpdate: (mac: string) => req<OpRef>('POST', `/machines/${mac}/labhost/update?ignoreWindow=true`),
  labReboot: (mac: string) => req<OpRef>('POST', `/machines/${mac}/labhost/reboot?ignoreWindow=true`),
  labAddVMs: (mac: string, r: VMPlan) => req<OpRef>('POST', `/machines/${mac}/labhost/vms`, r),
  labVM: (mac: string, name: string, action: 'start' | 'stop' | 'kill' | 'reprovision') => req<OpRef>('POST', `/machines/${mac}/labhost/vms/${name}/${action}`),
  labVMResize: (mac: string, name: string, cpus: number, memMiB: number) => req<void>('PUT', `/machines/${mac}/labhost/vms/${name}`, { cpus, memMiB }),
  labVMDelete: (mac: string, name: string) => req<void>('DELETE', `/machines/${mac}/labhost/vms/${name}`),
  design: (name: string, macs: string[], metallbRange?: string) => req<{ yaml: string; cluster: ClusterSpec; warnings: Warning[]; topology: { ControlPlanes: number; Workers: number; AllowScheduling: boolean; HA: boolean }; overlaps?: string[] }>('POST', '/config/design', { name, macs, metallbRange }),
  lint: (yaml: string) => req<{ warnings: Warning[]; yaml: string }>('POST', '/config/lint', { yaml }),
  renameNode: (cluster: string, hostname: string, to: string) => req<OpRef>('POST', `/clusters/${cluster}/nodes/${hostname}/rename?ignoreWindow=true`, { to }),
  moveNodePool: (cluster: string, hostname: string, pool: string) => req<OpRef>('POST', `/clusters/${cluster}/nodes/${hostname}/pool?ignoreWindow=true`, { pool }),
  readdressNode: (cluster: string, hostname: string, network: NodeNetwork | null, ip: string) => req<OpRef>('POST', `/clusters/${cluster}/nodes/${hostname}/readdress?ignoreWindow=true`, { network, ip }),
  certificates: (cluster: string) => req<CertInfo[]>('GET', `/clusters/${cluster}/certificates`),
  rotateCredential: (cluster: string, which: 'talosconfig' | 'kubeconfig') => req<OpRef>('POST', `/clusters/${cluster}/certificates/rotate`, { which }),
  maintenance: (cluster: string) => req<MaintenanceState>('GET', `/clusters/${cluster}/maintenance`),
  audit: (cluster?: string) => req<AuditEntry[]>('GET', '/audit' + (cluster ? `?cluster=${cluster}` : '')),
  testAlerts: () => req<{ ok: boolean; errors: string[] }>('POST', '/settings/alerts/test'),
  offsiteStatus: () => req<OffsiteStatus>('GET', '/settings/offsite'),
  offsiteTest: (t: OffsiteTarget) => req<{ ok: boolean; error?: string; roundTripMs?: number; target?: string }>('POST', '/settings/offsite/test', t),
  offsiteBackup: () => req<OpRef>('POST', '/settings/offsite/backup'),
  snapshots: (cluster: string) => req<Snapshot[]>('GET', `/clusters/${cluster}/snapshots`),
  takeSnapshot: (cluster: string) => req<OpRef>('POST', `/clusters/${cluster}/snapshots`, { source: 'manual' }),
  deleteSnapshot: (cluster: string, id: number) => req<void>('DELETE', `/clusters/${cluster}/snapshots/${id}`),
  verifySnapshot: (cluster: string, id: number) => req<{ ok: boolean; error?: string; snapshot: Snapshot }>('POST', `/clusters/${cluster}/snapshots/${id}/verify`),
  restoreSnapshot: (cluster: string, id: number) => req<OpRef>('POST', `/clusters/${cluster}/snapshots/${id}/restore?ignoreWindow=true`, { confirm: cluster }),
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
  applyCluster: (name: string, yaml?: string) => req<OpRef>('POST', `/clusters/${name}/apply?ignoreWindow=true`, { yaml: yaml || '' }),
  platformPlan: (name: string) => req<OpRef>('POST', `/clusters/${name}/platform/plan`),
  platformApply: (name: string) => req<OpRef>('POST', `/clusters/${name}/platform/apply`),
  platformApplyPlan: (name: string, planId: number) => req<OpRef>('POST', `/clusters/${name}/platform/apply/${planId}`),
  upgradeTalos: (name: string, to: string) => req<OpRef>('POST', `/clusters/${name}/upgrade/talos?ignoreWindow=true`, { to }),
  upgradeKubernetes: (name: string, to: string) => req<OpRef>('POST', `/clusters/${name}/upgrade/kubernetes?ignoreWindow=true`, { to }),
  exportCluster: (name: string, dir?: string) => req<{ dir: string }>('POST', `/clusters/${name}/export`, { dir: dir || '' }),
  addNode: (name: string, node: NodeSpec) => req<OpRef>('POST', `/clusters/${name}/nodes`, node),
  removeNode: (name: string, hostname: string, force = false) => req<OpRef>('DELETE', `/clusters/${name}/nodes/${hostname}?force=${force}&ignoreWindow=true`),
  nodes: (cluster?: string) => req<NodeRow[]>('GET', '/nodes' + (cluster ? `?cluster=${cluster}` : '')),
  discover: (targets: string[]) => req<OpRef>('POST', '/discover', { targets }),
  services: (ip: string) => req<Service[]>('GET', `/nodes/${ip}/services`),
  inventory: (ip: string) => req<Inventory>('GET', `/nodes/${ip}/inventory`),
  nodeKubernetes: (ip: string) => req<NodeDetail>('GET', `/nodes/${ip}/kubernetes`),
  cordon: (cluster: string, hostname: string) => req<OpRef>('POST', `/clusters/${cluster}/nodes/${hostname}/cordon`),
  uncordon: (cluster: string, hostname: string) => req<OpRef>('POST', `/clusters/${cluster}/nodes/${hostname}/uncordon`),
  drain: (cluster: string, hostname: string) => req<OpRef>('POST', `/clusters/${cluster}/nodes/${hostname}/drain?ignoreWindow=true`),
  rebootNode: (cluster: string, hostname: string, drain: boolean) => req<OpRef>('POST', `/clusters/${cluster}/nodes/${hostname}/reboot?ignoreWindow=true`, { drain }),
  upgradeNode: (cluster: string, hostname: string, to: string) => req<OpRef>('POST', `/clusters/${cluster}/nodes/${hostname}/upgrade?ignoreWindow=true`, { to }),
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
  uptime(sec: number) {
    const d = Math.floor(sec / 86400), h = Math.floor((sec % 86400) / 3600), m = Math.floor((sec % 3600) / 60)
    return d ? `${d}d ${h}h` : h ? `${h}h ${m}m` : `${m}m`
  },
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
      'node.rename': 'Rename node', 'node.pool': 'Move node to pool', 'node.readdress': 'Re-address node', 'machine.power': 'Remote power action', 'kubit.backup': 'Kubit backup off-site', 'labhost.provision': 'Install lab host', 'labhost.local': 'Lab host on this Mac', 'labhost.cluster': 'Lab cluster', 'labhost.vms': 'Add lab VMs', 'labhost.vm.start': 'Start VM', 'labhost.vm.stop': 'Stop VM', 'labhost.vm.kill': 'Force-stop VM', 'labhost.vm.reprovision': 'Re-provision VM', 'labhost.update': 'Update lab host', 'labhost.reboot': 'Reboot lab host',
      'etcd.snapshot': 'etcd snapshot', 'etcd.restore': 'Restore etcd from snapshot', 'cert.rotate': 'Rotate credential',
    } as Record<string, string>)[kind] || kind
  },
}
