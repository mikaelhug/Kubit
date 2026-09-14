// Global state as signals, fed by one SSE connection. Pages read signals and call api.*;
// the stream keeps operations, their steps and their event logs current.
import { signal, computed } from '@preact/signals'
import { api, fmt, getToken, type ClusterRow, type Event, type HealthEvent, type Message, type Operation, type Status, type Step } from './api'

export const clusters = signal<ClusterRow[]>([])
export const operations = signal<Map<number, Operation>>(new Map())
export const opEvents = signal<Map<number, Event[]>>(new Map())
export const connected = signal(false)
export const drawerOpen = signal<boolean>(read('kubit.drawer', false))
export const drawerHeight = signal<number>(read('kubit.drawerHeight', 260))
export const drawerTab = signal<number | null>(null)
export const toasts = signal<{ id: number; text: string; tone: 'info' | 'error' | 'good' }[]>([])
/** Latest Status per cluster, pushed by the daemon's watcher. */
export const statuses = signal<Map<string, Status>>(new Map())
/** Health events per cluster (newest first), seeded from the API and appended live. */
export const health = signal<Map<string, HealthEvent[]>>(new Map())

export async function loadHealth(name: string) {
  try {
    const list = await api.events(name)
    const m = new Map(health.value)
    m.set(name, list)
    health.value = m
  } catch {}
}

export async function ack(name: string, id?: number) {
  if (id === undefined) await api.ackAll(name); else await api.ackEvent(id)
  const m = new Map(health.value)
  m.set(name, (m.get(name) ?? []).map((e) => id === undefined || e.id === id ? { ...e, acked: true } : e))
  health.value = m
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
  try { clusters.value = await api.clusters() } catch {}
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

function upsertOp(o: Operation) {
  const m = new Map(operations.value)
  m.set(o.id, { ...m.get(o.id), ...o, steps: o.steps ?? m.get(o.id)?.steps ?? [] })
  operations.value = m
}

function applyStepEvent(id: number, e: Event) {
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

let source: EventSource | null = null
export function connect() {
  if (source) return
  const t = getToken()
  source = new EventSource('/api/v1/events' + (t ? `?token=${encodeURIComponent(t)}` : ''))
  source.onopen = () => { connected.value = true; reloadOperations(); reloadClusters() }
  source.onmessage = (ev) => {
    const m = JSON.parse(ev.data) as Message
    if (m.kind === 'operation' && m.operation) {
      const prev = operations.value.get(m.operation.id)
      upsertOp(m.operation)
      if (m.operation.status !== 'running') {
        reloadClusters()
        if (prev?.status === 'running') toast(`${fmt.kind(m.operation.kind)}${m.operation.cluster ? ' · ' + m.operation.cluster : ''}: ${m.operation.status}`, m.operation.status === 'done' ? 'good' : 'error')
      }
    } else if (m.kind === 'status' && m.status && m.cluster) {
      const sm = new Map(statuses.value)
      sm.set(m.cluster, m.status)
      statuses.value = sm
    } else if (m.kind === 'health' && m.health) {
      const hm = new Map(health.value)
      const resolves: Record<string, string> = { 'talos.back': 'talos.unreachable', 'node.ready': 'node.notready', 'api.back': 'api.unreachable', 'etcd.healthy': 'etcd.unhealthy', 'lb.assigned': 'lb.lost' }
      const cleared = resolves[m.health.kind]
      const h = m.health
      const prev = (hm.get(h.cluster) ?? []).map((e) => cleared && e.kind === cleared && (e.node ?? '') === (h.node ?? '') ? { ...e, acked: true } : e)
      hm.set(h.cluster, [h, ...prev].slice(0, 200))
      health.value = hm
      if (m.health.severity !== 'info') toast(`${m.health.cluster}: ${m.health.message}`, 'error')
    } else if (m.kind === 'event' && m.event && m.operationId !== undefined) {
      const e = m.event
      const id = m.operationId
      if (e.kind === 'log' || !e.kind) {
        const map = new Map(opEvents.value)
        const list = map.get(id) ?? []
        map.set(id, list.length > 2000 ? [...list.slice(-1500), e] : [...list, e])
        opEvents.value = map
      }
      applyStepEvent(id, e)
    }
  }
  source.onerror = () => {
    connected.value = false
    source?.close()
    source = null
    setTimeout(connect, 3000)
  }
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
