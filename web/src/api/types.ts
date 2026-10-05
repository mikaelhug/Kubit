export type Level = 'info' | 'warn' | 'error' | 'done'
export type StepStatus = 'pending' | 'running' | 'done' | 'failed' | 'skipped' | 'cancelled'

export interface Step { id: string; title: string; status: StepStatus; node?: string; startedAt?: string; finishedAt?: string }
export interface Event { seq?: number; time: string; clock?: string; kind?: 'log' | 'steps' | 'step'; level: Level; step: string; node?: string; message: string; steps?: Step[]; status?: StepStatus }
export type OpStatus = 'running' | 'done' | 'failed' | 'cancelled'
export interface Operation { id: number; cluster: string; kind: string; status: OpStatus; log?: string; startedAt: string; finishedAt?: string; steps: Step[]; artifact?: unknown; request?: unknown }
export interface Message { seq?: number; kind: 'hello' | 'stopped' | 'resync' | 'event' | 'operation' | 'status' | 'health' | 'refresh' | 'cluster' | 'clusterRemoved' | 'machine' | 'machineRemoved' | 'snapshot' | 'snapshotRemoved' | 'audit' | 'healthAck' | 'healthResolved' | 'versions' | 'observer'; observer?: ObserverState; operationId?: number; event?: Event; operation?: Operation; cluster?: string; status?: Status; health?: HealthEvent; scope?: string; clusterRow?: ClusterRow; machine?: NodeRow; snapshot?: Snapshot; audit?: AuditEntry; key?: string; node?: string; hello?: { seq: number; version: string; startedAt: string; pid: number; os?: string } }
export interface ObserverState { online: boolean; since?: string; error?: string; gaps24h: number; lastGapAt?: string }
export interface HealthEvent { id: number; ts: string; cluster: string; node?: string; severity: 'info' | 'warn' | 'critical'; kind: string; message: string; acked: boolean }
export interface ServiceHealth { collectedAt: string; metallb: boolean; workloads?: { kind: string; namespace: string; name: string; ready: number; desired: number; available: boolean; ageSec: number }[]; pods?: { namespace: string; name: string; node?: string; owner?: string; phase: string; restarts: number; ageSec: number }[]; claims?: { namespace: string; name: string; phase: string; ageSec: number }[]; services?: { namespace: string; name: string; type: string; hasSelector: boolean; endpoints: number; ageSec: number }[]; ingresses?: { namespace: string; name: string; hasAddress: boolean; ageSec: number }[]; pool?: { range: string; total: number; allocated: number } }
export interface Sample { ts: string; node?: string; cpuMilli: number; cpuCap: number; memBytes: number; memCap: number; pods: number; ready: boolean; reachable: boolean; disk?: number; diskCap?: number }
export interface SOPSKey { cluster: string; recipient: string; createdAt: string }
export interface Build { name: string; image?: string; state: 'running' | 'succeeded' | 'failed'; pod?: string; startedAt?: string; finishedAt?: string }
export interface FluxRepository { url: string; branch?: string; path?: string; interval?: string }
export interface FluxObject { kind: string; namespace: string; name: string; ready: 'True' | 'False' | 'Unknown'; reason?: string; message?: string; revision?: string; suspended?: boolean; since?: string }
export interface ImageStatus { talosVersion: string; installed: string; desired: string; extensions?: string[]; outdated: boolean }
export interface Namespace { name: string; phase: string; security?: string; ageSec: number; createdAt?: string; platform: boolean; addon?: string }
export interface Workload { kind: string; namespace: string; name: string; ready: number; desired: number; available: boolean; images: string; age: string; ageSec?: number; createdAt?: string; selector?: string }
export interface KService { namespace: string; name: string; type: string; clusterIP: string; externalIPs?: string[]; ports: string[]; endpoints: number; selector?: string; age: string; ageSec?: number; createdAt?: string }
export interface KIngress { namespace: string; name: string; class?: string; rules: { host: string; path: string; service: string; port: string }[]; addresses?: string[]; tlsHosts?: string[]; age: string; ageSec?: number; createdAt?: string }
export interface NetworkView { services: KService[]; ingresses: KIngress[]; pool?: { range: string; total: number; allocated: { ip: string; service: string }[] }; poolError?: string }
export interface StorageView { classes: { name: string; provisioner: string; default: boolean; reclaim: string; binding: string; expandable: boolean; createdAt?: string }[]; volumes: { name: string; capacityBytes: number; phase: string; class: string; claim?: string; accessModes: string; reclaim: string; age: string; createdAt?: string }[]; claims: { namespace: string; name: string; phase: string; requestedBytes: number; capacityBytes: number; class: string; volume?: string; age: string; ageSec?: number; createdAt?: string }[] }
export interface PodEvent { type: string; status: string; reason?: string; message?: string; since?: string }
export interface AddonStatus {
  key: string; enabled: boolean; address?: string; values?: Record<string, unknown>; pinnedVersion?: string
  release?: { name: string; namespace: string; chart: string; chartVersion: string; appVersion?: string; status: string; lastDeployed?: number }
  readiness?: { namespace: string; ready: number; total: number; detail?: string[] }
  state: 'disabled' | 'pending' | 'deploying' | 'ready' | 'degraded' | 'failed' | 'orphaned'
}
export interface PxeStatus { running: boolean; statusUrl: string; error?: string; command?: string; startedAt?: string; interface?: string; httpOnly?: boolean; ip?: string; httpPort?: number; talosVersion?: string; schematicId?: string; boots?: { mac: string; ip?: string; arch?: string; firstSeen: string; lastSeen: string; stage: string; count: number }[]; log?: string[] }
export interface Versions { talos: string[]; talosSource: string; kubernetesMinors: string[]; kubernetesLatest: string; machinery: string; minTalos: string; note: string }

export interface InstallDisk { path?: string; selector?: { minSize?: string; type?: string; model?: string } }
export interface NodeNetwork { addresses: string[]; gateway?: string; nameservers?: string[]; vlan?: number; mtu?: number }
export interface NodeSpec { hostname: string; ip: string; mac?: string; uuid?: string; pool?: string; role?: 'controlplane' | 'worker'; arch: string; kvm?: boolean; tpm?: boolean; watchdog?: boolean; installDisk?: InstallDisk; dataDisks?: string[]; network?: NodeNetwork; labels?: Record<string, string>; taints?: Record<string, string>; annotations?: Record<string, string>; patches?: Record<string, unknown>[] }
export interface Pool { name: string; role: 'controlplane' | 'worker'; labels?: Record<string, string>; taints?: Record<string, string>; extensions?: string[]; installDisk?: InstallDisk }
export interface AddonSpec { enabled: boolean; values?: Record<string, unknown> }
export interface Snapshot { id: number; cluster: string; ts: string; node: string; sizeBytes: number; sha256: string; keys: number; talosVersion?: string; k8sVersion?: string; source: 'manual' | 'schedule' | 'pre-upgrade'; status: 'ok' | 'corrupt' | 'missing' }
export interface PlatformSpec { metallb: AddonSpec & { range?: string }; traefik: AddonSpec; gvisor: AddonSpec; metricsServer: AddonSpec; certManager: AddonSpec; flux: AddonSpec & { repository?: FluxRepository }; longhorn: AddonSpec; builds: AddonSpec }
export interface ClusterSpec {
  apiVersion: string; kind: string; metadata: { name: string }
  spec: {
    talosVersion: string; kubernetesVersion: string; extensions?: string[]; schematicID?: string
    controlPlane: { endpoint: string; vip?: string; allowScheduling?: boolean }
    network: { podCIDR: string; serviceCIDR: string; nameservers?: string[]; ntp?: string[]; policies?: boolean; discovery?: boolean; firewall?: boolean }
    pools?: Pool[]
    nodes: NodeSpec[]
    platform: PlatformSpec
    backup?: { schedule?: string; s3?: { bucket?: string; region?: string; endpoint?: string; prefix?: string; pathStyle?: boolean }; ageRecipients?: string[]; compression?: boolean }
    maintenance?: { window?: string; timezone?: string }
    storage?: { systemDisk?: boolean; ephemeralSize?: string; encryption?: 'tpm' | 'nodeID' }
    patches?: Record<string, unknown>[]
  }
}
export interface CertInfo { name: string; subject: string; issuer?: string; notBefore: string; notAfter: string; daysLeft: number; rotatable: boolean; error?: string }
export interface AuditEntry { id: number; at: string; cluster: string; action: string; detail: string; actor?: string }
export interface MaintenanceState { window: string; timezone: string; open: boolean; next?: string; closes?: string }
export interface ClusterRow { name: string; state: string; schematicId: string; createdAt: string; updatedAt: string; spec: ClusterSpec }

export interface Inventory {
  ip: string; hostname?: string; uuid?: string; serial?: string; cpus: number; memoryBytes: number; kvm: boolean; tpm?: boolean; watchdog?: boolean; virtual?: boolean; arch: string; talosVersion: string; platform: string; stage: string; manufacturer?: string; product?: string
  disks: { devPath: string; sizeBytes: number; model?: string; transport?: string; rotational: boolean; readonly: boolean; cdrom: boolean; serial?: string; wwid?: string; links?: string[]; key?: string }[]
  links: { name: string; mac: string; up: boolean; addresses?: string[] }[]
  bootTime?: string; extensions?: { name: string; version: string; author?: string }[]
  etcd?: { memberId: string; leader: boolean; learner: boolean; dbSizeBytes: number; dbInUseBytes: number; raftIndex: number; raftTerm: number; errors?: string[] }
}
export interface ConfigStatus { behind: string[] }
export interface Resources { cpuMilli: number; memBytes: number; pods: number }
export interface PodSummary { namespace: string; name: string; node?: string; containers?: string[]; phase: string; ready: string; restarts: number; owner?: string; cpuMilli: number; memBytes: number; age: string; ageSec?: number; createdAt?: string; usageCpuMilli?: number; usageMemBytes?: number }
export interface NodeDetail {
  name: string; ready: boolean; unschedulable: boolean; kubeletVersion: string; containerRuntime: string; kernel: string; osImage: string; internalIP: string
  conditions: { type: string; status: string; reason?: string; message?: string; since?: string }[] | null
  taints: string[] | null; labels: Record<string, string> | null; capacity: Resources; allocatable: Resources; requests: Resources; pods: PodSummary[] | null
}
export type MachineKind = 'member' | 'maintenance' | 'configured' | 'booting' | 'unbooted'
export interface NodeRow { ip: string; mac: string; uuid?: string; serial?: string; ipsSeen?: string[]; cluster: string; hostname: string; pool: string; arch: string; role: string; source: string; state: string; kind: MachineKind; talos: boolean; talosVersion: string; firstSeen: string; lastSeen: string; inventory?: Inventory }

export interface NodeStatus { hostname: string; ip: string; role: string; pool: string; seenAt?: string; arch: string; kvm: boolean; talosVersion: string; kubeletVersion: string; ready: boolean; unschedulable: boolean; talosReachable: boolean; talosError?: string; talosReach?: string; registered: boolean; stage: string; cpuMilli: number; cpuCapMilli: number; memBytes: number; memCapBytes: number; memAllocBytes: number; pods: number; podCap: number; gvisor: boolean }
export interface Status {
  name: string; state: string; talosVersion: string; kubernetesVersion: string; endpoint: string; apiReachable: boolean; apiError?: string; apiReach?: string; health?: 'healthy' | 'degraded' | 'down' | 'unknown'; openAlerts?: number
  observer?: 'online' | 'offline'; observerError?: string; lastContactAt?: string
  nodes: NodeStatus[]
  etcd: { members: number; expected: number; healthy: boolean; leader?: string; alarms?: string[] }
  totals: { cpuMilli: number; cpuCapMilli: number; memBytes: number; memCapBytes: number; pods: number; podCap: number; nodesReady: number; nodes: number }
  platform?: { appliedAt?: string; outputs?: Record<string, string>; error?: string }
  observedAt?: string; lastSnapshotAt?: string
}
export interface Service { id: string; state: string; healthy: boolean; unknown?: boolean; last: string }

export type OpRef = { operationId: number }
export interface SecretKey { path: string[]; encrypted: boolean; list?: boolean }
export interface SecretFile { path: string; recipients: string[]; keys: SecretKey[]; kind?: string; name?: string; namespace?: string; error?: string }
export interface SecretRepo { index: number; dir: string; name: string; cluster?: string; error?: string; files: SecretFile[] }
