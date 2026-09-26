import { batch, computed, signal, type ReadonlySignal, type Signal } from '@preact/signals'
import { api, setUnauthorizedHandler, type AuditEntry, type ClusterRow, type Event, type HealthEvent, type Me, type NodeRow, type ObserverState, type Operation, type Role, type Sample, type Settings, type Snapshot, type Status, type Step, type Versions } from './api'
import { persist, read } from './local'
import { copyMap, editMap, setIn } from './maps'

export const kubitKey = 'kubit'

export const clusters = signal<ClusterRow[]>([])
export const machines = signal<Map<string, NodeRow>>(new Map())
export const machineList = computed(() => [...machines.value.values()].sort((a, b) => a.ip.localeCompare(b.ip, undefined, { numeric: true })))
export const labHosts = computed(() => machineList.value.filter((m) => m.labhost))
export const snapshots = signal<Map<string, Snapshot[]>>(new Map())
export const audit = signal<AuditEntry[]>([])
export const settings = signal<Settings | null>(null)
export const versions = signal<Versions | null>(null)
export const me = signal<Me | null | undefined>(undefined)
export const authState = signal<{ setup: boolean; users: number; sso?: string }>({ setup: false, users: 0 })
export async function loadMe() {
  try {
    const m = await api.me()
    me.value = m
    authState.value = { setup: m.setup, users: m.users, sso: m.sso }
  } catch (e: any) {
    me.value = null
    if (e && typeof e === 'object' && 'body' in e && e.body) authState.value = { setup: !!e.body.setup, users: e.body.users ?? 0, sso: e.body.sso }
    if (e && typeof e === 'object' && 'status' in e && e.status !== 401) throw e
  }
}
setUnauthorizedHandler(() => { if (me.value !== null) me.value = null })
const rank: Record<Role, number> = { viewer: 1, operator: 2, admin: 3 }
export const can = (role: Role) => { const r = me.value?.role; return !!r && rank[r] >= rank[role] }
export const daemon = signal<{ version: string; startedAt: string; service: boolean; os?: string } | null>(null)
export const operations = signal<Map<number, Operation>>(new Map())
export const opEvents = signal<Map<number, Event[]>>(new Map())
export const connected = signal(false)
export const resyncing = signal(false)
export const reconnectAttempt = signal(0)
export const drawerOpen = signal<boolean>(read('kubit.drawer', false))
export const drawerHeight = signal<number>(read('kubit.drawerHeight', 260))
export const drawerTab = signal<number | null>(null)
export const toasts = signal<{ id: number; text: string; tone: 'info' | 'error' | 'good' }[]>([])
export const statuses = signal<Map<string, Status>>(new Map())
export const hostSamples = signal<Map<string, Sample>>(new Map())
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

export async function ack(name: string, id?: number) {
  if (id === undefined) await api.ackAll(name); else await api.ackEvent(id)
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

export async function loadSettings() {
  try { settings.value = await api.settings() } catch {}
}

export const opList = computed(() => [...operations.value.values()])
export const running = computed(() => opList.value.filter((o) => o.status === 'running').sort((a, b) => a.id - b.id))
export const runningCount = computed(() => running.value.length)
const opsByCluster = computed(() => {
  const m = new Map<string, Operation[]>()
  for (const o of opList.value) {
    const list = m.get(o.cluster)
    if (list) list.push(o); else m.set(o.cluster, [o])
  }
  return m
})
export function opsFor(cluster: string): Operation[] { return opsByCluster.value.get(cluster) ?? [] }
export function runningFor(cluster: string) { return opsFor(cluster).filter((o) => o.status === 'running') }

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

export async function reloadOperations() {
  try {
    const list = await api.operations()
    editMap(operations, (m) => { for (const o of list) m.set(o.id, { ...m.get(o.id), ...o }) })
  } catch {}
}

export function watch(op: { operationId: number } | number, open = true) {
  const id = typeof op === 'number' ? op : op.operationId
  drawerTab.value = id
  if (open) setDrawer(true)
  if (!operations.value.has(id)) api.operation(id).then((o) => upsertOp(o)).catch(() => {})
}

export function upsertOp(o: Operation) {
  operations.value = copyMap(operations.value, (m) => m.set(o.id, { ...m.get(o.id), ...o, steps: o.steps ?? m.get(o.id)?.steps ?? [] }))
}

const finalStatus = new Set(['done', 'failed', 'skipped', 'cancelled'])

function nextSteps(steps: Step[], e: Event): Step[] | null {
  if (e.kind === 'steps' && e.steps) {
    const added = e.steps.filter((s) => !steps.some((x) => x.id === s.id))
    return added.length ? [...steps, ...added] : null
  }
  const i = steps.findIndex((s) => s.id === e.step)
  if (e.kind === 'step' && e.status) {
    if (i < 0) return [...steps, { id: e.step, title: e.step, status: e.status, startedAt: e.time, ...(finalStatus.has(e.status) ? { finishedAt: e.time } : {}) }]
    const cur = steps[i]
    const next = { ...cur, status: e.status, startedAt: cur.startedAt ?? e.time, finishedAt: finalStatus.has(e.status) ? e.time : cur.finishedAt }
    if (next.status === cur.status && next.startedAt === cur.startedAt && next.finishedAt === cur.finishedAt) return null
    return steps.map((s, j) => (j === i ? next : s))
  }
  if (!e.step) return null
  if (i < 0) return [...steps, { id: e.step, title: e.step, status: 'running', startedAt: e.time }]
  if (steps[i].status !== 'pending') return null
  return steps.map((s, j) => (j === i ? { ...s, status: 'running', startedAt: e.time } : s))
}

export function applyStepEvent(id: number, e: Event) {
  const op = operations.value.get(id)
  if (!op) return
  const steps = nextSteps(op.steps ?? [], e)
  if (steps) upsertOp({ ...op, steps })
}

let eventSeq = 0
export const nextEventSeq = () => ++eventSeq

export async function loadOperationLog(id: number) {
  const o = await api.operation(id)
  upsertOp(o)
  if (o.log && !opEvents.value.has(id)) {
    const lines = o.log.split('\n').filter(Boolean).map((line): Event => {
      const m = /^(\d\d:\d\d:\d\d) \[([^\]]+)\] (?:([^:]+): )?(.*)$/.exec(line)
      const level: Event['level'] = line.startsWith('error:') ? 'error' : 'info'
      const seq = nextEventSeq()
      return m ? { seq, time: '', clock: m[1], level, step: m[2], node: m[3], message: m[4] } : { seq, time: '', level, step: '', message: line }
    })
    setIn(opEvents, id, lines)
  }
  return o
}
