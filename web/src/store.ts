import { batch, computed, signal, type ReadonlySignal, type Signal } from '@preact/signals'
import { api, type AuditEntry, type ClusterRow, type HealthEvent, type NodeRow, type ObserverState, type Snapshot, type Status, type Versions } from './api'
import { persist, read } from './local'
import { editMap, setIn } from './maps'

export const kubitKey = 'kubit'

export const clusters = signal<ClusterRow[]>([])
export const machines = signal<Map<string, NodeRow>>(new Map())
export const machineList = computed(() => [...machines.value.values()].sort((a, b) => a.ip.localeCompare(b.ip, undefined, { numeric: true })))
export const snapshots = signal<Map<string, Snapshot[]>>(new Map())
export const audit = signal<AuditEntry[]>([])
export const versions = signal<Versions | null>(null)
export const daemon = signal<{ version: string; startedAt: string; os?: string } | null>(null)
export const connected = signal(false)
export const stopped = signal(false)
export const resyncing = signal(false)
export const live = computed(() => connected.value && !resyncing.value)
export const reconnectAttempt = signal(0)
export const drawerOpen = signal<boolean>(read('kubit.drawer', false))
export const drawerHeight = signal<number>(read('kubit.drawerHeight', 260))
export const drawerTab = signal<number | null>(null)
export const toasts = signal<{ id: number; text: string; tone: 'info' | 'error' | 'good' }[]>([])
export const statuses = signal<Map<string, Status>>(new Map())
export const observer = signal<ObserverState>({ online: true, gaps24h: 0 })
export const health = signal<Map<string, HealthEvent[]>>(new Map())

const refreshes = new Map<string, Signal<number>>()
function refreshSignal(key: string) {
  let s = refreshes.get(key)
  if (!s) { s = signal(0); refreshes.set(key, s) }
  return s
}
export function refreshKey(cluster: string, scope: string) { return refreshSignal(`${cluster}/${scope}`).value }
export function bumpRefresh(cluster: string, scope: string) { refreshSignal(`${cluster}/${scope}`).value++ }
export function bumpAllRefreshes() {
  batch(() => {
    for (const s of refreshes.values()) s.value++
    refreshSignal('*/resync').value++
  })
}

export function setDrawer(open: boolean) {
  drawerOpen.value = open
  persist('kubit.drawer', open)
}

export async function loadObserver() {
  try { observer.value = await api.observer() } catch {}
}

export async function loadVersions() {
  try { versions.value = await api.versions() } catch {}
}

const isOpen = (e: HealthEvent) => !e.acked && e.severity !== 'info'

export function openAlerts(key: string) { return (health.value.get(key) ?? []).filter(isOpen) }

const alertIndexes = new Map<string, ReadonlySignal<Map<string, HealthEvent>>>()

export function alertIndex(cluster: string) {
  let index = alertIndexes.get(cluster)
  if (!index) {
    index = computed(() => {
      const m = new Map<string, HealthEvent>()
      for (const e of health.value.get(cluster) ?? []) if (isOpen(e) && e.node && !m.has(e.node)) m.set(e.node, e)
      return m
    })
    alertIndexes.set(cluster, index)
  }
  return index.value
}

export const objectKey = (kind: string, ns: string, name: string) => `${kind}/${ns}/${name}`

export async function loadAllHealth(keys: string[]) {
  const lists = await Promise.all(keys.map((k) => api.events(k).catch(() => null)))
  editMap(health, (m) => lists.forEach((list, i) => { if (list) m.set(keys[i], list) }))
}

export async function loadHealth(name: string) {
  try { setIn(health, name, await api.events(name)) } catch {}
}

export function ack(name: string, id?: number) {
  return (id === undefined ? api.ackAll(name) : api.ackEvent(id)).catch((e) => toast(e.message, 'error'))
}

export function upsertCluster(row: ClusterRow) {
  const rest = clusters.value.filter((c) => c.name !== row.name)
  clusters.value = [...rest, row].sort((a, b) => a.name.localeCompare(b.name))
}

export const machineKey = (m: NodeRow) => m.mac || `ip:${m.ip}`

export async function loadMachines() {
  try {
    const rows = await api.nodes()
    machines.value = new Map(rows.map((m) => [machineKey(m), m]))
  } catch {}
}

export async function loadSnapshots(name: string) {
  try { setIn(snapshots, name, await api.snapshots(name)) } catch {}
}

export async function loadAudit(cluster?: string) {
  try {
    const rows = await api.audit(cluster)
    if (cluster) {
      const others = audit.value.filter((a) => a.cluster !== cluster)
      audit.value = [...rows, ...others].sort((a, b) => b.id - a.id)
    } else audit.value = rows
  } catch {}
}


let toastSeq = 0
export function toast(text: string, tone: 'info' | 'error' | 'good' = 'info') {
  const id = ++toastSeq
  toasts.value = [...toasts.value, { id, text, tone }]
  setTimeout(() => { toasts.value = toasts.value.filter((t) => t.id !== id) }, tone === 'error' ? 8000 : 4000)
}

export async function reloadClusters() {
  try {
    clusters.value = await api.clusters()
    const rows = await Promise.all(clusters.value.map((c) => api.status(c.name).catch(() => null)))
    editMap(statuses, (m) => rows.forEach((s, i) => { if (s) m.set(clusters.value[i].name, s) }))
  } catch {}
}
