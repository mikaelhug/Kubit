import { authedUrl, req } from './api/http'
import type { AddonStatus, Build, CertInfo, ClusterRow, ConfigStatus, FluxObject, HealthEvent, ImageStatus, Inventory, Namespace, NetworkView, NodeDetail, NodeRow, ObserverState, PodEvent, PodSummary, PxeStatus, SecretRepo, SOPSKey, Sample, Service, ServiceHealth, Snapshot, Status, StorageView, Versions, Workload } from './api/types'

export type * from './api/types'
export { authedUrl, getToken } from './api/http'
export { fmt, splitList } from './api/format'

export const api = {
  observer: () => req<ObserverState>('GET', '/observer'),
  clusters: () => req<ClusterRow[]>('GET', '/clusters'),
  status: (name: string, fresh = false) => req<Status>('GET', `/clusters/${name}/status${fresh ? '?fresh=true' : ''}`),
  samples: (name: string, range = '24h', node = '') => req<Sample[]>('GET', `/clusters/${name}/samples?range=${range}&node=${encodeURIComponent(node)}`),
  events: (name: string, unacked = false) => req<HealthEvent[]>('GET', `/clusters/${name}/events?limit=200&unacked=${unacked}`),
  ackEvent: (id: number) => req<void>('POST', `/events/${id}/ack`),
  ackAll: (name: string) => req<void>('POST', `/clusters/${name}/events/ack`),
  versions: () => req<Versions>('GET', '/versions'),
  serviceHealth: (name: string) => req<{ latest: ServiceHealth | null; alerts: HealthEvent[] }>('GET', `/clusters/${name}/service-health`),
  certificates: (cluster: string) => req<CertInfo[]>('GET', `/clusters/${cluster}/certificates`),
  snapshots: (cluster: string) => req<Snapshot[]>('GET', `/clusters/${cluster}/snapshots`),
  stopDaemon: () => req<void>('POST', '/daemon/stop'),
  pxe: () => req<PxeStatus>('GET', '/pxe'),
  addons: (name: string) => req<AddonStatus[]>('GET', `/clusters/${name}/addons`),
  workloads: (name: string) => req<Workload[]>('GET', `/clusters/${name}/workloads`),
  namespaces: (name: string) => req<Namespace[]>('GET', `/clusters/${name}/namespaces`),
  imageStatus: (name: string) => req<ImageStatus>('GET', `/clusters/${name}/image`),
  configStatus: (name: string) => req<ConfigStatus>('GET', `/clusters/${name}/config`),
  sopsKey: (name: string) => req<SOPSKey>('GET', `/clusters/${name}/sops`),
  flux: (name: string) => req<FluxObject[]>('GET', `/clusters/${name}/flux`),
  builds: (name: string) => req<Build[]>('GET', `/clusters/${name}/builds`),
  pods: (name: string, namespace = '', selector = '') => req<PodSummary[]>('GET', `/clusters/${name}/pods?namespace=${encodeURIComponent(namespace)}&selector=${encodeURIComponent(selector)}`),
  podEvents: (name: string, ns: string, pod: string) => req<PodEvent[]>('GET', `/clusters/${name}/pods/${ns}/${pod}/events`),
  network: (name: string) => req<NetworkView>('GET', `/clusters/${name}/network`),
  storage: (name: string) => req<StorageView>('GET', `/clusters/${name}/storage`),
  clusterYaml: (name: string) => req<{ dir: string; yaml: string }>('GET', `/clusters/${name}/yaml`),
  nodes: (cluster?: string) => req<NodeRow[]>('GET', '/nodes' + (cluster ? `?cluster=${cluster}` : '')),
  discover: (targets: string[]) => req<{ found: number }>('POST', '/discover', { targets }),
  services: (ip: string) => req<Service[]>('GET', `/nodes/${ip}/services`),
  inventory: (ip: string) => req<Inventory>('GET', `/nodes/${ip}/inventory`),
  nodeKubernetes: (ip: string) => req<NodeDetail>('GET', `/nodes/${ip}/kubernetes`),
  secrets: () => req<SecretRepo[]>('GET', '/secrets'),
  secretValue: (repo: number, file: string, key: string[]) => req<{ value: string }>('GET', `/secrets/value?${secretQuery(repo, file, key)}`),
  setSecret: (repo: number, file: string, key: string[], value: string) => req<void>('PUT', '/secrets/value', { repo, file, key, value }),
  deleteSecret: (repo: number, file: string, key: string[]) => req<void>('DELETE', `/secrets/value?${secretQuery(repo, file, key)}`),
  newSecret: (repo: number, file: string, name: string, namespace: string) => req<void>('POST', '/secrets/files', { repo, file, name, namespace }),
}

function secretQuery(repo: number, file: string, key: string[]) {
  const q = new URLSearchParams({ repo: String(repo), file })
  for (const k of key) q.append('key', k)
  return q.toString()
}

export const kubeconfigUrl = (cluster: string) => authedUrl(`/clusters/${cluster}/kubeconfig`)
export const snapshotUrl = (cluster: string, id: number) => authedUrl(`/clusters/${cluster}/snapshots/${id}`)

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
