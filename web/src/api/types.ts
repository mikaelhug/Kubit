
export interface Message { seq?: number; kind: 'hello' | 'stopped' | 'resync' | 'status' | 'health' | 'refresh' | 'cluster' | 'machine' | 'versions' | 'apply' | 'plan'; cluster?: string; status?: Status; health?: HealthEvent; scope?: string; clusterRow?: ClusterRow; machine?: NodeRow; hello?: { seq: number; version: string; startedAt: string; os?: string }; line?: ApplyLine; plan?: PlanSummary }
export interface PlanChange { action: string; target?: string; detail?: string; blocked?: string }
export interface PlanInstall { hostname: string; role: string; ip: string; disk: string }
export interface Plan { hash: string; cluster: string; changes: PlanChange[]; installs?: PlanInstall[]; problems?: string[] }
export type PlanState = 'checking' | 'ready' | 'blocked' | 'applying' | 'failed'
export interface PlanSummary { cluster: string; state: PlanState; hash?: string; changes: number; oneTime: number; problems: number; plannedAt?: string; holder?: string; error?: string }
export interface ApplyLine { ts: string; level: 'info' | 'warn' | 'error' | 'done'; step: string; node?: string; message: string }
export interface ApplyRun { running: boolean; started?: string; finished?: string; error?: string; lines: ApplyLine[] }
export interface HealthEvent { id: number; ts: string; cluster: string; node?: string; severity: 'info' | 'warn' | 'critical'; kind: string; message: string; open: boolean }
export interface Sample { ts: string; node?: string; cpuMilli: number; cpuCap: number; memBytes: number; memCap: number; pods: number; ready: boolean; reachable: boolean }
export interface SOPSKey { recipient: string }
export interface Build { name: string; image?: string; state: 'running' | 'succeeded' | 'failed'; pod?: string; startedAt?: string; finishedAt?: string }
export interface FluxRepository { url: string; branch?: string; path?: string; interval?: string }
export interface FluxObject { kind: string; namespace: string; name: string; ready: 'True' | 'False' | 'Unknown'; reason?: string; message?: string; revision?: string; suspended?: boolean; since?: string }
export interface ImageStatus { talosVersion: string; installed: string; desired: string; extensions?: string[]; outdated: boolean }
export interface Namespace { name: string; phase: string; security?: string; ageSec: number; createdAt?: string; platform: boolean; addon?: string }
export interface Workload { kind: string; namespace: string; name: string; ready: number; desired: number; available: boolean; images: string; age: string; ageSec?: number; createdAt?: string; selector?: string }
export interface KService { namespace: string; name: string; type: string; clusterIP: string; externalIPs?: string[]; ports: string[]; endpoints: number; selector?: string; age: string; ageSec?: number; createdAt?: string }
export interface KIngress { namespace: string; name: string; class?: string; rules: { host: string; path: string; service: string; port: string }[]; addresses?: string[]; tlsHosts?: string[]; age: string; ageSec?: number; createdAt?: string }
export interface KRoute { namespace: string; name: string; hostnames: string[]; gateways: string[]; rules: { path: string; backends: string[] }[]; accepted: string; message?: string; age: string; ageSec?: number; createdAt?: string }
export interface NetworkView { services: KService[]; ingresses: KIngress[]; routes: KRoute[]; routesError?: string; pool?: { range: string; total: number; allocated: { ip: string; service: string }[] }; poolError?: string }
export interface StorageView { classes: { name: string; provisioner: string; default: boolean; reclaim: string; binding: string; expandable: boolean; createdAt?: string }[]; volumes: { name: string; capacityBytes: number; phase: string; class: string; claim?: string; accessModes: string; reclaim: string; age: string; createdAt?: string }[]; claims: { namespace: string; name: string; phase: string; requestedBytes: number; capacityBytes: number; class: string; volume?: string; age: string; ageSec?: number; createdAt?: string }[] }
export interface PodEvent { type: string; status: string; reason?: string; message?: string; since?: string }
export interface AddonStatus {
  key: string; enabled: boolean; address?: string; values?: Record<string, unknown>; pinnedVersion?: string
  release?: { name: string; namespace: string; chart: string; chartVersion: string; appVersion?: string; status: string; lastDeployed?: number }
  readiness?: { namespace: string; ready: number; total: number; detail?: string[] }
  state: 'disabled' | 'pending' | 'deploying' | 'ready' | 'degraded' | 'failed' | 'orphaned'
}
export interface PxeStatus { running: boolean; statusUrl: string; error?: string; command?: string; startedAt?: string; interface?: string; httpOnly?: boolean; ip?: string; httpPort?: number; talosVersion?: string; schematicId?: string; boots?: { mac: string; ip?: string; arch?: string; firstSeen: string; lastSeen: string; stage: string; count: number }[]; log?: string[] }
export interface Versions { talos: string[]; kubernetesLatest: string; minTalos: string }

export interface InstallDisk { path?: string; selector?: { minSize?: string; type?: string; model?: string } }
export interface NodeNetwork { addresses: string[]; gateway?: string; nameservers?: string[]; vlan?: number; mtu?: number }
export interface NodeSpec { hostname: string; ip: string; mac?: string; uuid?: string; role?: 'controlplane' | 'worker'; arch: string; kvm?: boolean; installDisk?: InstallDisk; dataDisks?: string[]; network?: NodeNetwork; labels?: Record<string, string>; taints?: Record<string, string>; annotations?: Record<string, string>; patches?: Record<string, unknown>[] }
export interface AddonSpec { enabled: boolean; values?: Record<string, unknown> }
export interface Snapshot { id: string; ts: string; source: string; sizeBytes: number }
export interface PlatformSpec { metallb: AddonSpec & { range?: string }; traefik: AddonSpec; gvisor: { enabled: boolean }; metricsServer: AddonSpec; certManager: AddonSpec; flux: AddonSpec & { repository?: FluxRepository }; longhorn: AddonSpec; builds: { enabled: boolean } }
export interface ClusterSpec {
  apiVersion: string; kind: string; metadata: { name: string }
  spec: {
    talosVersion: string; kubernetesVersion: string; extensions?: string[]; schematicID?: string
    controlPlane: { endpoint: string; vip?: string; allowScheduling?: boolean }
    network: { podCIDR: string; serviceCIDR: string; nameservers?: string[]; ntp?: string[]; policies?: boolean; discovery?: boolean }
    nodes: NodeSpec[]
    platform: PlatformSpec
    backup?: { schedule?: string; s3?: { bucket?: string; region?: string; endpoint?: string; prefix?: string; pathStyle?: boolean }; ageRecipients?: string[]; compression?: boolean }
    storage?: { systemDisk?: boolean; ephemeralSize?: string }
    patches?: Record<string, unknown>[]
  }
}
export interface CertInfo { name: string; subject: string; issuer?: string; notBefore: string; notAfter: string; daysLeft: number; rotatable: boolean; error?: string }
export interface ClusterRow { name: string; state: string; spec: ClusterSpec; hash: string }

export interface Inventory {
  ip: string; hostname?: string; uuid?: string; serial?: string; cpus: number; memoryBytes: number; kvm: boolean; virtual?: boolean; arch: string; talosVersion: string; platform: string; stage: string; manufacturer?: string; product?: string
  disks: { devPath: string; sizeBytes: number; model?: string; transport?: string; rotational: boolean; readonly: boolean; cdrom: boolean; serial?: string; wwid?: string; links?: string[] }[]
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
export type MachineKind = 'member' | 'maintenance' | 'configured' | 'offline'
export interface NodeRow { ip: string; mac: string; uuid?: string; serial?: string; ipsSeen?: string[]; cluster: string; hostname: string; arch: string; role: string; state: string; kind: MachineKind; talos: boolean; talosVersion: string; lastSeen: string; inventory?: Inventory; declared?: { cluster: string; hostname: string } }

export interface NodeStatus { hostname: string; ip: string; role: string; seenAt?: string; arch: string; talosVersion: string; kubeletVersion: string; ready: boolean; unschedulable: boolean; talosReachable: boolean; talosError?: string; talosReach?: string; registered: boolean; stage: string; cpuMilli: number; cpuCapMilli: number; memBytes: number; memCapBytes: number; memAllocBytes: number; pods: number; podCap: number; temperatures?: Temperature[] }
export interface Temperature { sensor: 'cpu' | 'disk'; chip: string; celsius: number; high?: number; critical?: number }
export interface Status {
  name: string; state: string; talosVersion: string; kubernetesVersion: string; endpoint: string; apiReachable: boolean; apiError?: string; apiReach?: string; health?: 'healthy' | 'degraded' | 'down' | 'unknown'; openAlerts?: number
  observer?: 'online' | 'offline'; observerError?: string; lastContactAt?: string
  nodes: NodeStatus[]
  etcd: { members: number; expected: number; healthy: boolean; leader?: string; alarms?: string[] }
  totals: { cpuMilli: number; cpuCapMilli: number; memBytes: number; memCapBytes: number; pods: number; podCap: number; nodesReady: number; nodes: number }
  ingressIP?: string
  observedAt?: string; lastSnapshotAt?: string
}
export interface Service { id: string; state: string; healthy: boolean; unknown?: boolean; last: string }

export interface SecretKey { path: string[]; encrypted: boolean; list?: boolean }
export interface SecretFile {
  path: string
  recipients: string[]
  keys: SecretKey[]
  kind?: string
  type?: string
  name?: string
  namespace?: string
  error?: string
  hash: string
  repo: number
  git?: string
  modified?: string
  cluster?: string
  fluxReads: boolean
  skipped?: string
}
export interface SecretRepo { index: number; dir: string; name: string; cluster?: string; error?: string; files: SecretFile[]; fluxOf: string[]; fluxRecipient?: string; fluxReads: boolean }
export interface SecretIndex { repos: SecretRepo[]; labels: Record<string, string> }
export interface SecretSource { repo: number; name: string; dir: string; cluster: boolean; flux: boolean; reads: boolean; error?: string }
export interface FluxSource { enabled: boolean; url?: string; branch?: string; path?: string }
export interface ClusterSecrets { flux: FluxSource; recipient?: string; sources: SecretSource[]; files: SecretFile[]; labels: Record<string, string> }
export interface SecretEntry { path: string[]; value: string }
export interface SecretSpec { name: string; namespace: string; type: string; stringData: Record<string, string> }

export interface MachineChoice {
  mac: string
  role?: string
  address?: string
  gateway?: string
  nameservers?: string[]
}

export interface LiveNet { address?: string; gateway?: string; nameservers?: string[] }

export interface NodeNetworkView { hash: string; hostname: string; role: string; ip: string; declared?: NodeNetwork; live: LiveNet; endpoint: string; endpointFollows: boolean; clusterNameservers?: string[] }

export interface DesignRequest {
  dir?: string
  name?: string
  vip?: string
  machines: MachineChoice[]
  hash?: string
}

export interface DesignNode { hostname: string; ip: string; mac: string; role: string; disk: string; diskBytes?: number; network?: NodeNetwork; live: LiveNet; inUse?: boolean }

export interface DesignView {
  cluster: string
  dir: string
  new: boolean
  nodes: DesignNode[]
  vip?: string
  vipInUse?: boolean
  endpoint: string
  talosVersion: string
  kubernetesVersion: string
  warnings: string[]
  hash?: string
}

export interface GitState { repo: boolean; branch?: string; upstream?: string; ahead: number; changes: { status: string; path: string }[] }
export interface RepoView { dir: string; git: GitState }

export interface DiscoverState {
  subnets: string[]
  pingSeconds: number
  scanning: boolean
  lastScanAt?: string
  everySeconds: number
}
