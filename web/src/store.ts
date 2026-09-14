// Global state as signals, fed by one SSE connection. Pages read signals and call api.*;
// the stream keeps operations, their steps and their event logs current.
import { signal, computed } from '@preact/signals'
import { api, getToken, type ClusterRow, type Event, type Message, type Operation, type Step } from './api'

export const clusters = signal<ClusterRow[]>([])
export const operations = signal<Map<number, Operation>>(new Map())
export const opEvents = signal<Map<number, Event[]>>(new Map())
export const connected = signal(false)
export const drawerOpen = signal<boolean>(read('kubit.drawer', false))
export const drawerHeight = signal<number>(read('kubit.drawerHeight', 260))
export const drawerTab = signal<number | null>(null)
export const toasts = signal<{ id: number; text: string; tone: 'info' | 'error' | 'good' }[]>([])

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
        if (prev?.status === 'running') toast(`${fmtKind(m.operation.kind)} ${m.operation.status}`, m.operation.status === 'done' ? 'good' : 'error')
      }
    } else if (m.kind === 'event' && m.event) {
      const e = m.event
      if (e.kind === 'log' || !e.kind) {
        const map = new Map(opEvents.value)
        const list = map.get(m.operationId) ?? []
        map.set(m.operationId, list.length > 2000 ? [...list.slice(-1500), e] : [...list, e])
        opEvents.value = map
      }
      applyStepEvent(m.operationId, e)
    }
  }
  source.onerror = () => {
    connected.value = false
    source?.close()
    source = null
    setTimeout(connect, 3000)
  }
}

function fmtKind(k: string) {
  return ({ 'cluster.create': 'Create cluster', 'cluster.apply': 'Apply', 'platform.plan': 'Plan', 'platform.apply': 'Apply add-ons', 'upgrade.talos': 'Talos upgrade', 'upgrade.kubernetes': 'Kubernetes upgrade', 'node.add': 'Add node', 'node.remove': 'Remove node', discover: 'Discovery' } as Record<string, string>)[k] || k
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
