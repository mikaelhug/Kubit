export type Level = 'info' | 'warn' | 'error' | 'done'
export type StepStatus = 'pending' | 'running' | 'done' | 'failed' | 'skipped' | 'cancelled'

export interface Step { id: string; title: string; status: StepStatus; node?: string; startedAt?: string; finishedAt?: string }
export interface Event { seq?: number; time: string; clock?: string; kind?: 'log' | 'steps' | 'step'; level: Level; step: string; node?: string; message: string; steps?: Step[]; status?: StepStatus }
export type OpStatus = 'running' | 'done' | 'failed' | 'cancelled'
export interface Operation { id: number; cluster: string; kind: string; status: OpStatus; log?: string; startedAt: string; finishedAt?: string; steps: Step[]; artifact?: unknown; request?: unknown }
export interface Message { seq?: number; kind: 'hello' | 'resync' | 'event' | 'operation' | 'status' | 'health' | 'refresh' | 'cluster' | 'clusterRemoved' | 'machine' | 'machineRemoved' | 'snapshot' | 'snapshotRemoved' | 'audit' | 'settings' | 'healthAck' | 'healthResolved' | 'versions' | 'hostSample' | 'observer'; observer?: ObserverState; operationId?: number; event?: Event; operation?: Operation; cluster?: string; status?: Status; health?: HealthEvent; scope?: string; clusterRow?: ClusterRow; machine?: NodeRow; snapshot?: Snapshot; audit?: AuditEntry; settings?: Settings; sample?: Sample; key?: string; node?: string; hello?: { seq: number; version: string; startedAt: string; service: boolean; pid: number; os?: string } }
export interface ObserverState { online: boolean; since?: string; error?: string; gaps24h: number; lastGapAt?: string }
export interface HealthEvent { id: number; ts: string; cluster: string; node?: string; severity: 'info' | 'warn' | 'critical'; kind: string; message: string; acked: boolean }
export interface ServiceHealth { collectedAt: string; metallb: boolean; workloads?: { kind: string; namespace: string; name: string; ready: number; desired: number; available: boolean; ageSec: number }[]; pods?: { namespace: string; name: string; node?: string; owner?: string; phase: string; restarts: number; ageSec: number }[]; claims?: { namespace: string; name: string; phase: string; ageSec: number }[]; services?: { namespace: string; name: string; type: string; hasSelector: boolean; endpoints: number; ageSec: number }[]; ingresses?: { namespace: string; name: string; hasAddress: boolean; ageSec: number }[]; pool?: { range: string; total: number; allocated: number } }
export interface Sample { ts: string; node?: string; cpuMilli: number; cpuCap: number; memBytes: number; memCap: number; pods: number; ready: boolean; reachable: boolean; disk?: number; diskCap?: number }
export interface SOPSKey { cluster: string; recipient: string; createdAt: string }
export interface Build { name: string; image?: string; state: 'running' | 'succeeded' | 'failed'; pod?: string; startedAt?: string; finishedAt?: string }
export interface FluxRepository { url: string; branch?: string; path?: string; interval?: string }
export interface FluxObject { kind: string; namespace: string; name: string; ready: 'True' | 'False' | 'Unknown'; reason?: string; message?: string; revision?: string; suspended?: boolean; since?: string }
export interface ImageStatus { talosVersion: string; installed: string; desired: string; extensions?: string[]; outdated: boolean }
export interface Namespace { name: string; phase: string; security?: string; ageSec: number; platform: boolean; addon?: string }
export interface Workload { kind: string; namespace: string; name: string; ready: number; desired: number; available: boolean; images: string; age: string; selector?: string }
export interface KService { namespace: string; name: string; type: string; clusterIP: string; externalIPs?: string[]; ports: string[]; endpoints: number; selector?: string; age: string }
export interface KIngress { namespace: string; name: string; class?: string; rules: { host: string; path: string; service: string; port: string }[]; addresses?: string[]; tlsHosts?: string[]; age: string }
export interface NetworkView { services: KService[]; ingresses: KIngress[]; pool?: { range: string; total: number; allocated: { ip: string; service: string }[] }; poolError?: string }
export interface StorageView { classes: { name: string; provisioner: string; default: boolean; reclaim: string; binding: string; expandable: boolean }[]; volumes: { name: string; capacityBytes: number; phase: string; class: string; claim?: string; accessModes: string; reclaim: string; age: string }[]; claims: { namespace: string; name: string; phase: string; requestedBytes: number; capacityBytes: number; class: string; volume?: string; age: string }[] }
export interface PodEvent { type: string; status: string; reason?: string; message?: string; since?: string }
export interface AddonStatus {
  key: string; enabled: boolean; address?: string; values?: Record<string, unknown>; pinnedVersion?: string
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
export interface PlatformSpec { metallb: AddonSpec & { range?: string }; ingressNginx: AddonSpec; gvisor: AddonSpec; metricsServer: AddonSpec; certManager: AddonSpec; flux: AddonSpec & { repository?: FluxRepository }; longhorn: AddonSpec; builds: AddonSpec }
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
    storage?: { systemDisk?: boolean; ephemeralSize?: string }
  }
}
export interface ClusterOIDC { issuer: string; clientID: string; usernameClaim?: string; usernamePrefix?: string; groupsClaim?: string; groupsPrefix?: string; adminGroup?: string }
export interface ClusterForm { oidc?: ClusterOIDC | null; talosVersion: string; kubernetesVersion: string; endpoint: string; vip: string; allowScheduling: boolean | null; podCIDR: string; serviceCIDR: string; extensions: string[]; nameservers: string[]; ntp: string[]; etcdSnapshotInterval: string; etcdSnapshotKeep: number; maintenanceWindow: string; maintenanceTimezone: string }
export interface CertInfo { name: string; subject: string; issuer?: string; notBefore: string; notAfter: string; daysLeft: number; rotatable: boolean; error?: string }
export interface AuditEntry { id: number; at: string; cluster: string; action: string; detail: string; actor?: string }
export interface MaintenanceState { window: string; timezone: string; open: boolean; next?: string; closes?: string }
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
  conditions: { type: string; status: string; reason?: string; message?: string; since?: string }[] | null
  taints: string[] | null; labels: Record<string, string> | null; capacity: Resources; allocatable: Resources; requests: Resources; pods: PodSummary[] | null
}
export interface OOBConfig { type: '' | 'amt' | 'redfish'; host: string; user: string; password: string; tls: boolean }
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
export interface LabHost { state: 'installing' | 'setup' | 'ready' | 'updating' | 'error'; error?: string; capacity: LabCapacity; talos?: string; vms: LabVM[] | null; metrics?: LabMetrics; updates?: LabUpdates; install?: LabInstall; network?: 'bridge' | 'routed'; disk?: string; boot?: LabBootLine; failures?: number; driver?: 'libvirt' | 'vfkit'; iso?: string; updatedAt: string }

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
export interface Service { id: string; state: string; healthy: boolean; unknown?: boolean; last: string }

export interface AttrDiff { key: string; before?: string; after?: string; unknown?: boolean; sensitive?: boolean }
export interface PlanChange { address: string; type: string; name: string; action: 'create' | 'update' | 'replace' | 'delete'; attrs?: AttrDiff[] }
export interface PlanDiff { summary: { Add: number; Change: number; Remove: number }; groups: { addon: string; changes: PlanChange[] }[]; warnings?: string[]; timestamp: string }
export type Role = 'viewer' | 'operator' | 'admin'
export interface Me { user: string; role: Role; via: string; setup: boolean; users: number; sso?: string }
export interface OIDCSettings { enabled: boolean; name: string; issuer: string; clientId: string; clientSecret: string; usernameClaim: string; groupsClaim: string; adminGroups: string[]; operatorGroups: string[]; viewerGroups: string[]; defaultRole: '' | Role }
export interface User { id: number; name: string; role: Role; disabled: boolean; source: string; createdAt: string; lastLogin?: string; hasPassword: boolean }
export interface ApiToken { name: string; kind: string; createdAt: string; expiresAt?: string; lastUsed?: string; prefix: string }
export type OpRef = { operationId: number }
