import { batch, computed, effect, signal, type Signal } from '@preact/signals'
import { api, type ApplyRun, type ClusterRow, type HealthEvent, type NodeRow, type PlanSummary, type Status, type Versions } from './api'
import { editMap, setIn } from './maps'

export const kubitKey = 'kubit'

export const clusters = signal<ClusterRow[]>([])
export const machines = signal<Map<string, NodeRow>>(new Map())
export const machineList = computed(() => [...machines.value.values()].sort((a, b) => a.ip.localeCompare(b.ip, undefined, { numeric: true })))
export const versions = signal<Versions | null>(null)
export const daemon = signal<{ version: string; startedAt: string; os?: string } | null>(null)
export const connected = signal(false)
export const stopped = signal(false)
export const resyncing = signal(false)
export const live = computed(() => connected.value && !resyncing.value)
export const reconnectAttempt = signal(0)
export const toasts = signal<{ id: number; text: string; tone: 'info' | 'error' | 'good'; until: number }[]>([])
export const statuses = signal<Map<string, Status>>(new Map())
export const health = signal<Map<string, HealthEvent[]>>(new Map())
export const applyRuns = signal<Map<string, ApplyRun>>(new Map())
export const plans = signal<Map<string, PlanSummary>>(new Map())

export async function loadPlans() {
  try { plans.value = new Map((await api.plans()).map((p) => [p.cluster, p])) } catch {}
}

export async function loadApplyRun(name: string) {
  try { setIn(applyRuns, name, await api.applyRun(name)) } catch {}
}

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

export async function loadVersions() {
  try { versions.value = await api.versions() } catch {}
}

const isOpen = (e: HealthEvent) => e.open

export function openAlerts(key: string) { return (health.value.get(key) ?? []).filter(isOpen) }

async function loadAllHealth(keys: string[]) {
  const lists = await Promise.all(keys.map((k) => api.events(k).catch(() => null)))
  editMap(health, (m) => lists.forEach((list, i) => { if (list) m.set(keys[i], list) }))
}

const clusterHash = (name: string) => clusters.value.find((c) => c.name === name)?.hash ?? ''

export async function writeClusterYaml(name: string, write: (hash: string) => Promise<{ hash: string }>) {
  const { hash } = await write(clusterHash(name))
  await new Promise<void>((resolve) => {
    let stop = () => {}
    stop = effect(() => { if (clusterHash(name) === hash) { resolve(); queueMicrotask(() => stop()) } })
  })
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

let toastSeq = 0
export function toast(text: string, tone: 'info' | 'error' | 'good' = 'info') {
  const id = ++toastSeq
  const at = Date.now()
  toasts.value = [...toasts.value.filter((t) => t.until > at), { id, text, tone, until: at + (tone === 'error' ? 8000 : 4000) }]
}

export async function reloadClusters() {
  try {
    clusters.value = await api.clusters()
    const names = clusters.value.map((c) => c.name)
    const rows = await Promise.all(names.map((n) => api.status(n).catch(() => null)))
    editMap(statuses, (m) => rows.forEach((s, i) => { if (s) m.set(names[i], s) }))
    await loadAllHealth([kubitKey, ...names])
  } catch {}
}
