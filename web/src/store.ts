// Global live state as signals, fed by one WebSocket (see live.ts). Pages derive from
// these and only fetch large derived views, which the daemon tells them to refresh.
import { signal, computed } from '@preact/signals'
import { api, setUnauthorizedHandler, type Me, type AuditEntry, type ClusterRow, type Event, type HealthEvent, type NodeRow, type Operation, type Sample, type Settings, type Snapshot, type Status, type Step } from './api'

export const clusters = signal<ClusterRow[]>([])
/** Every machine Kubit knows, keyed by MAC; pushed on each store write. */
export const machines = signal<Map<string, NodeRow>>(new Map())
export const machineList = computed(() => [...machines.value.values()].sort((a, b) => a.ip.localeCompare(b.ip, undefined, { numeric: true })))
/** etcd snapshots per cluster, newest first. */
export const snapshots = signal<Map<string, Snapshot[]>>(new Map())
/** Audit entries, newest first (all clusters; filter per view). */
export const audit = signal<AuditEntry[]>([])
/** Kubit settings as the daemon last pushed them (secrets redacted). */
export const settings = signal<Settings | null>(null)
/** Who is signed in: undefined until asked, null when sign-in is required. */
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
export const can = (role: 'viewer' | 'operator' | 'admin') => { const r = me.value?.role; return !!r && ({ viewer: 1, operator: 2, admin: 3 })[r] >= ({ viewer: 1, operator: 2, admin: 3 })[role] }
/** Newest stable Talos the factory publishes; bumps when the daemon's hourly check changes. */
export const latestTalos = signal<string>('')
/** Daemon facts from the hello message. */
export const daemon = signal<{ version: string; startedAt: string; service: boolean } | null>(null)
export const operations = signal<Map<number, Operation>>(new Map())
export const opEvents = signal<Map<number, Event[]>>(new Map())
export const connected = signal(false)
/** True while base state is being reloaded after a reconnect or an external write. */
export const resyncing = signal(false)
export const reconnectAttempt = signal(0)
export const drawerOpen = signal<boolean>(read('kubit.drawer', false))
export const drawerHeight = signal<number>(read('kubit.drawerHeight', 260))
export const drawerTab = signal<number | null>(null)
export const toasts = signal<{ id: number; text: string; tone: 'info' | 'error' | 'good' }[]>([])
/** Latest Status per cluster, pushed by the daemon's watcher. */
export const statuses = signal<Map<string, Status>>(new Map())
/** Per (cluster, scope) change counters pushed by the daemon; views refetch when theirs moves. */
export const refreshes = signal<Map<string, number>>(new Map())
export function refreshKey(cluster: string, scope: string) { return refreshes.value.get(`${cluster}/${scope}`) ?? 0 }
/** Newest reading per lab host (by MAC); the Lab host tab appends it to its history. */
export const hostSamples = signal<Map<string, Sample>>(new Map())
/** Health events per cluster (newest first), seeded from the API and appended live. */
export const health = signal<Map<string, HealthEvent[]>>(new Map())

/** Open (unacked, unresolved) workload alert for one object, keyed as kind/namespace/name. */
export function openAlert(cluster: string, kind: string, ns: string, name: string): HealthEvent | undefined {
  const key = `${kind}/${ns}/${name}`
  return (health.value.get(cluster) ?? []).find((e) => !e.acked && e.node === key && e.severity !== 'info')
}

/** `?ns=` from the URL, used by the object links on alert rows. */
export function nsFromQuery(): string {
  return typeof location !== 'undefined' ? new URLSearchParams(location.search).get('ns') ?? '' : ''
}

export async function loadAllHealth(keys: string[]) {
  const lists = await Promise.all(keys.map((k) => api.events(k).catch(() => null)))
  const m = new Map(health.value)
  lists.forEach((list, i) => { if (list) m.set(keys[i], list) })
  health.value = m
}

export async function loadHealth(name: string) {
  try {
    const list = await api.events(name)
    const m = new Map(health.value)
    m.set(name, list)
    health.value = m
  } catch {}
}

/** Acks go to the daemon; the healthAck message that comes back updates every tab. */
export async function ack(name: string, id?: number) {
  if (id === undefined) await api.ackAll(name); else await api.ackEvent(id)
}

export function upsertCluster(row: ClusterRow) {
  const rest = clusters.value.filter((c) => c.name !== row.name)
  clusters.value = [...rest, row].sort((a, b) => a.name.localeCompare(b.name))
}

export async function loadMachines() {
  try {
    const rows = await api.nodes()
    machines.value = new Map(rows.map((m) => [m.mac || `ip:${m.ip}`, m]))
  } catch {}
}

export async function loadSnapshots(name: string) {
  try {
    const rows = await api.snapshots(name)
    const m = new Map(snapshots.value)
    m.set(name, rows)
    snapshots.value = m
  } catch {}
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

export const running = computed(() => [...operations.value.values()].filter((o) => o.status === 'running').sort((a, b) => a.id - b.id))
export const recent = computed(() => [...operations.value.values()].sort((a, b) => b.id - a.id).slice(0, 100))

function read<T>(key: string, fallback: T): T {
  try { const v = localStorage.getItem(key); return v === null ? fallback : JSON.parse(v) } catch { return fallback }
}
export function persist(key: string, v: unknown) { try { localStorage.setItem(key, JSON.stringify(v)) } catch {} }

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
    const sm = new Map(statuses.value)
    rows.forEach((s, i) => { if (s) sm.set(clusters.value[i].name, s) })
    statuses.value = sm
  } catch {}
}

export async function reloadOperations() {
  try {
    const list = await api.operations()
    const m = new Map(operations.value)
    for (const o of list) m.set(o.id, { ...m.get(o.id), ...o })
    operations.value = m
  } catch {}
}

/** Open the activity drawer on an operation and make it the focused tab. */
export function watch(op: { operationId: number } | number, open = true) {
  const id = typeof op === 'number' ? op : op.operationId
  drawerTab.value = id
  if (open) { drawerOpen.value = true; persist('kubit.drawer', true) }
  if (!operations.value.has(id)) api.operation(id).then((o) => upsertOp(o)).catch(() => {})
}

export function upsertOp(o: Operation) {
  const m = new Map(operations.value)
  m.set(o.id, { ...m.get(o.id), ...o, steps: o.steps ?? m.get(o.id)?.steps ?? [] })
  operations.value = m
}

export function applyStepEvent(id: number, e: Event) {
  const op = operations.value.get(id)
  if (!op) return
  let steps: Step[] = [...(op.steps || [])]
  if (e.kind === 'steps' && e.steps) {
    for (const s of e.steps) if (!steps.find((x) => x.id === s.id)) steps.push(s)
  } else if (e.kind === 'step' && e.status) {
    const i = steps.findIndex((s) => s.id === e.step)
    const now = e.time
    if (i < 0) steps.push({ id: e.step, title: e.step, status: e.status, startedAt: now })
    else steps[i] = { ...steps[i], status: e.status, startedAt: steps[i].startedAt ?? now, finishedAt: ['done', 'failed', 'skipped', 'cancelled'].includes(e.status) ? now : steps[i].finishedAt }
  } else if (e.step) {
    const i = steps.findIndex((s) => s.id === e.step)
    if (i < 0) steps.push({ id: e.step, title: e.step, status: 'running', startedAt: e.time })
    else if (steps[i].status === 'pending') steps[i] = { ...steps[i], status: 'running', startedAt: e.time }
  }
  upsertOp({ ...op, steps })
}

/** Load the persisted log of an operation that finished before this page opened. */
export async function loadOperationLog(id: number) {
  const o = await api.operation(id)
  upsertOp(o)
  if (o.log && !opEvents.value.has(id)) {
    const lines = o.log.split('\n').filter(Boolean).map((line): Event => {
      const m = /^(\d\d:\d\d:\d\d) \[([^\]]+)\] (?:([^:]+): )?(.*)$/.exec(line)
      const level: Event['level'] = line.startsWith('error:') ? 'error' : 'info'
      return m ? { time: '', clock: m[1], level, step: m[2], node: m[3], message: m[4] } : { time: '', level, step: '', message: line }
    })
    const map = new Map(opEvents.value)
    map.set(id, lines)
    opEvents.value = map
  }
  return o
}
