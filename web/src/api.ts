import { authedUrl, req } from './api/http'
import type { AppsRepo, AppsRequest, AppsReview, AppsStatus, AddonStatus, Build, CertInfo, ApplyRun, ClusterRow, ConfigStatus, DesignRequest, DesignView, DirListing, DiscoverState, FluxObject, FluxRepository, HealthEvent, ImageStatus, Inventory, Namespace, NetworkView, NodeDetail, NodeNetworkView, NodeRow, PodEvent, Plan, PlanSummary, PodSummary, PxeStatus, RepoView, SecretEntry, SecretIndex, SecretSpec, ClusterSecrets, DeployKey, SOPSKey, Sample, Service, Snapshot, Status, StorageView, Versions, Workload } from './api/types'

export type * from './api/types'
export { getToken } from './api/http'
export { fmt, splitList } from './api/format'

export const api = {
  clusters: () => req<ClusterRow[]>('GET', '/clusters'),
  design: (cluster: string, body: DesignRequest) => req<DesignView>('POST', '/design', { ...body, cluster: cluster || undefined }),
  designChecks: (cluster: string, body: DesignRequest) => req<DesignView>('POST', '/design/checks', { ...body, cluster: cluster || undefined }),
  createRepo: (body: DesignRequest) => req<DesignView>('POST', '/repos', body),
  dirs: (path: string) => req<DirListing>('GET', `/dirs?path=${encodeURIComponent(path)}`),
  inspectApps: (dir: string) => req<AppsRepo>('GET', `/apps?dir=${encodeURIComponent(dir)}`),
  appsStatus: (cluster: string) => req<AppsStatus>('GET', `/clusters/${cluster}/apps`),
  reviewApps: (cluster: string, body: AppsRequest) => req<AppsReview>('POST', `/clusters/${cluster}/apps/review`, body),
  connectApps: (cluster: string, body: AppsRequest & { hash: string }) => req<{ hash: string }>('POST', `/clusters/${cluster}/apps`, body),
  addNodes: (cluster: string, body: DesignRequest) => req<DesignView>('POST', `/clusters/${cluster}/nodes`, body),
  repo: (cluster: string) => req<RepoView>('GET', `/clusters/${cluster}/repo`),
  nodeNetwork: (cluster: string, host: string) => req<NodeNetworkView>('GET', `/clusters/${cluster}/nodes/${host}/network`),
  setNodeNetwork: (cluster: string, host: string, body: { static: boolean; address?: string; gateway?: string; nameservers?: string[]; hash: string }) => req<{ hash: string }>('PUT', `/clusters/${cluster}/nodes/${host}/network`, body),
  removeNode: (cluster: string, hostname: string, hash: string) => req<{ hash: string }>('DELETE', `/clusters/${cluster}/nodes/${hostname}?hash=${hash}`),
  setAddon: (cluster: string, key: string, body: { enabled: boolean; range?: string; repository?: FluxRepository; imageAutomation?: boolean; hash: string }) => req<{ hash: string }>('PUT', `/clusters/${cluster}/platform/${key}`, body),
  setVersions: (cluster: string, body: { talosVersion?: string; kubernetesVersion?: string; hash: string }) => req<{ hash: string }>('PUT', `/clusters/${cluster}/versions`, body),
  plans: () => req<PlanSummary[]>('GET', '/plans'),
  plan: (cluster: string) => req<{ summary: PlanSummary; plan: Plan | null }>('GET', `/clusters/${cluster}/plan`),
  replan: (cluster: string) => req<void>('POST', `/clusters/${cluster}/plan`),
  apply: (cluster: string, allowRemoval: boolean, planHash: string) => req<void>('POST', `/clusters/${cluster}/apply`, { allowRemoval, planHash }),
  applyRun: (cluster: string) => req<ApplyRun>('GET', `/clusters/${cluster}/apply`),
  destroy: (cluster: string, name: string) => req<void>('POST', `/clusters/${cluster}/destroy`, { name }),
  status: (name: string) => req<Status>('GET', `/clusters/${name}/status`),
  checkNow: (name: string) => req<void>('POST', `/clusters/${name}/check`),
  samples: (name: string, range = '24h', node = '') => req<Sample[]>('GET', `/clusters/${name}/samples?range=${range}&node=${encodeURIComponent(node)}`),
  events: (name: string) => req<HealthEvent[]>('GET', `/clusters/${name}/events?limit=200`),
  versions: () => req<Versions>('GET', '/versions'),
  certificates: (cluster: string) => req<CertInfo[]>('GET', `/clusters/${cluster}/certificates`),
  snapshots: (cluster: string) => req<Snapshot[]>('GET', `/clusters/${cluster}/snapshots`),
  takeSnapshot: (cluster: string) => req<Snapshot>('POST', `/clusters/${cluster}/snapshots`),
  stopDaemon: () => req<void>('POST', '/daemon/stop'),
  pxe: () => req<PxeStatus>('GET', '/pxe'),
  addons: (name: string) => req<AddonStatus[]>('GET', `/clusters/${name}/addons`),
  workloads: (name: string) => req<Workload[]>('GET', `/clusters/${name}/workloads`),
  namespaces: (name: string) => req<Namespace[]>('GET', `/clusters/${name}/namespaces`),
  imageStatus: (name: string) => req<ImageStatus>('GET', `/clusters/${name}/image`),
  configStatus: (name: string) => req<ConfigStatus>('GET', `/clusters/${name}/config`),
  sopsKey: (name: string) => req<SOPSKey>('GET', `/clusters/${name}/sops`),
  letFluxDecrypt: (name: string, repo: number) => req<{ rekeyed: number }>('POST', `/clusters/${name}/sops/flux?repo=${repo}`),
  flux: (name: string) => req<FluxObject[]>('GET', `/clusters/${name}/flux`),
  deployKey: (name: string) => req<DeployKey>('GET', `/clusters/${name}/flux/key`),
  newDeployKey: (name: string, hash: string, hostsOnly: boolean) => req<DeployKey>('POST', `/clusters/${name}/flux/key`, { hash, hostsOnly }),
  builds: (name: string) => req<Build[]>('GET', `/clusters/${name}/builds`),
  pods: (name: string, namespace = '', selector = '') => req<PodSummary[]>('GET', `/clusters/${name}/pods?namespace=${encodeURIComponent(namespace)}&selector=${encodeURIComponent(selector)}`),
  podEvents: (name: string, ns: string, pod: string) => req<PodEvent[]>('GET', `/clusters/${name}/pods/${ns}/${pod}/events`),
  network: (name: string) => req<NetworkView>('GET', `/clusters/${name}/network`),
  storage: (name: string) => req<StorageView>('GET', `/clusters/${name}/storage`),
  clusterYaml: (name: string) => req<{ dir: string; yaml: string; hash: string }>('GET', `/clusters/${name}/yaml`),
  saveClusterYaml: (name: string, yaml: string, hash: string) => req<{ hash: string }>('PUT', `/clusters/${name}/yaml`, { yaml, hash }),
  nodes: (cluster?: string) => req<NodeRow[]>('GET', '/nodes' + (cluster ? `?cluster=${cluster}` : '')),
  discover: (targets: string[]) => req<void>('POST', '/discover', { targets }),
  discoverState: () => req<DiscoverState>('GET', '/discover'),
  services: (ip: string) => req<Service[]>('GET', `/nodes/${ip}/services`),
  inventory: (ip: string) => req<Inventory>('GET', `/nodes/${ip}/inventory`),
  nodeKubernetes: (ip: string) => req<NodeDetail>('GET', `/nodes/${ip}/kubernetes`),
  secrets: () => req<SecretIndex>('GET', '/secrets'),
  clusterSecrets: (name: string) => req<ClusterSecrets>('GET', `/clusters/${name}/secrets`),
  secretValues: (repo: number, file: string) => req<{ hash: string; values: SecretEntry[] }>('GET', `/secrets/values?${secretQuery(repo, file)}`),
  patchSecret: (repo: number, file: string, hash: string, set: SecretEntry[], remove: string[][]) => req<void>('PATCH', '/secrets/file', { repo, file, hash, set, remove }),
  deleteSecretFile: (repo: number, file: string, hash: string) => req<{ kustomizations: string[] }>('DELETE', `/secrets/file?${secretQuery(repo, file, hash)}`),
  moveSecret: (repo: number, file: string, to: string, hash: string) => req<{ kustomizations: string[] }>('POST', '/secrets/move', { repo, file, to, hash }),
  newSecret: (repo: number, file: string, spec: SecretSpec) => req<{ kustomizations: string[] }>('POST', '/secrets/files', { repo, file, ...spec }),
}

function secretQuery(repo: number, file: string, hash?: string) {
  const q = new URLSearchParams({ repo: String(repo), file })
  if (hash) q.set('hash', hash)
  return q.toString()
}

export const kubeconfigUrl = (cluster: string) => authedUrl(`/clusters/${cluster}/kubeconfig`)
export const snapshotUrl = (cluster: string, id: string) => authedUrl(`/clusters/${cluster}/snapshots/${id}`)

export function podLogsUrl(cluster: string, ns: string, pod: string, container: string, follow: boolean, tail = 500) {
  const p = new URLSearchParams({ container, tail: String(tail) })
  if (follow) p.set('follow', 'true')
  return authedUrl(`/clusters/${cluster}/pods/${ns}/${pod}/logs`, p)
}

export function logsUrl(ip: string, service?: string, follow = false) {
  const p = new URLSearchParams()
  if (service) p.set('service', service)
  if (follow) p.set('follow', 'true')
  return authedUrl(`/nodes/${ip}/logs`, p)
}
