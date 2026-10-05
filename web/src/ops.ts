import { computed, signal } from '@preact/signals'
import { api, type Event, type OpRef, type Operation, type Step } from './api'
import { copyMap, editMap, setIn } from './maps'
import { connected, drawerTab, setDrawer } from './store'

export const operations = signal<Map<number, Operation>>(new Map())
export const opEvents = signal<Map<number, Event[]>>(new Map())

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

export async function reloadOperations() {
  try {
    const list = await api.operations()
    editMap(operations, (m) => { for (const o of list) m.set(o.id, mergeOp(m.get(o.id), o)) })
  } catch {}
}

export function watch(op: OpRef | number, open = true) {
  const id = typeof op === 'number' ? op : op.operationId
  drawerTab.value = id
  if (open) setDrawer(true)
  if (!operations.value.has(id)) ensureLog(id).catch(() => {})
}


const finalStatus = new Set(['done', 'failed', 'skipped', 'cancelled'])
const stepRank: Record<string, number> = { pending: 0, running: 1 }

const progress = (o: Operation) => (finalStatus.has(o.status) ? 1e6 : 0) + (o.steps ?? []).reduce((n, s) => n + (stepRank[s.status] ?? 2), 0)

export function mergeOp(cur: Operation | undefined, { log: _log, ...next }: Operation): Operation {
  const merged = { ...cur, ...next, steps: next.steps ?? cur?.steps ?? [] }
  return cur && progress(cur) > progress(merged) ? cur : merged
}

export function upsertOp(o: Operation) {
  operations.value = copyMap(operations.value, (m) => m.set(o.id, mergeOp(m.get(o.id), o)))
}

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

const maxLines = 2000
const keepLines = 1500
const keptOps = 20

let eventSeq = 0
const pending = new Map<number, Event[]>()
let scheduled = false
const viewing = new Map<number, number>()
const logLoaded = new Set<number>()
const logLoading = new Map<number, Promise<Operation>>()
const fetchedOffline = new Set<number>()
const logSince = new Map<number, number>()

const capped = (list: Event[]) => (list.length > maxLines ? list.slice(-keepLines) : list)

function schedule(fn: () => void) {
  if (typeof requestAnimationFrame === 'function' && typeof document !== 'undefined' && !document.hidden) requestAnimationFrame(fn)
  else setTimeout(fn, 100)
}

export function pushEvent(id: number, event: Event) {
  const e = { ...event, seq: ++eventSeq }
  if (e.kind === 'log' || !e.kind) {
    const list = pending.get(id)
    if (list) list.push(e); else pending.set(id, [e])
    if (!scheduled) { scheduled = true; schedule(flushEvents) }
  }
  applyStepEvent(id, e)
}

export function flushEvents() {
  scheduled = false
  if (!pending.size) return
  editMap(opEvents, (m) => { for (const [id, list] of pending) m.set(id, capped([...(m.get(id) ?? []), ...list])) })
  pending.clear()
  evict()
}

function evict() {
  const m = opEvents.value
  if (m.size <= keptOps) return
  const drop = [...m.keys()].filter((id) => !viewing.has(id) && operations.value.get(id)?.status !== 'running').sort((a, b) => a - b).slice(0, m.size - keptOps)
  if (!drop.length) return
  editMap(opEvents, (mm) => { for (const id of drop) { mm.delete(id); logLoaded.delete(id); logSince.delete(id) } })
}

function parseLine(line: string, seq: number): Event {
  const m = /^(\d\d:\d\d:\d\d) \[([^\]]*)\] (?:([^:]+): )?(.*)$/.exec(line)
  const level: Event['level'] = line.startsWith('error:') ? 'error' : 'info'
  return m ? { seq, time: '', clock: m[1], level, step: m[2], node: m[3], message: m[4] } : { seq, time: '', level, step: '', message: line }
}

export function parseLog(log: string): Event[] {
  return log.split('\n').filter(Boolean).map((line) => parseLine(line, ++eventSeq))
}

const lineKey = (e: Event) => `${e.node ? e.node + ': ' : ''}${e.step ? e.message : e.message.replace(/^error: /, '')}`

function lineKeys(e: Event): string[] {
  const [first, ...rest] = e.message.split('\n')
  return [lineKey({ ...e, message: first }), ...rest.filter(Boolean).map((line) => lineKey(parseLine(line, 0)))]
}

export function mergeLog(stored: Event[], live: Event[]): Event[] {
  const a = stored.map(lineKey)
  const perEvent = live.map(lineKeys)
  const b = perEvent.flat()
  let total = 0
  const ends = perEvent.map((keys) => (total += keys.length))
  for (let j = live.length; j > 0; j--) {
    const k = ends[j - 1]
    if (k > a.length) continue
    let same = true
    for (let i = 0; i < k && same; i++) same = a[a.length - k + i] === b[i]
    if (same) return [...stored, ...live.slice(j)]
  }
  return [...stored, ...live]
}

export function loadOperationLog(id: number): Promise<Operation> {
  const inflight = logLoading.get(id)
  if (inflight) return inflight
  if (!connected.value) fetchedOffline.add(id)
  const p: Promise<Operation> = api.operation(id).then((o) => {
    if (logLoading.get(id) !== p) return o
    upsertOp(o)
    flushEvents()
    const since = logSince.get(id) ?? 0
    setIn(opEvents, id, capped(mergeLog(parseLog(o.log ?? ''), (opEvents.value.get(id) ?? []).filter((e) => (e.seq ?? 0) > since))))
    logSince.delete(id)
    logLoaded.add(id)
    evict()
    return o
  }).finally(() => { if (logLoading.get(id) === p) logLoading.delete(id) })
  logLoading.set(id, p)
  return p
}

function staleLogs(ids: Iterable<number>) {
  const list = [...ids]
  for (const id of list) { logLoaded.delete(id); logLoading.delete(id); fetchedOffline.delete(id); logSince.set(id, eventSeq) }
  return Promise.all(list.filter((id) => viewing.has(id)).map((id) => loadOperationLog(id).catch(() => {})))
}

export function reloadLogs() {
  return staleLogs(new Set([...opEvents.value.keys(), ...pending.keys(), ...logLoaded, ...logLoading.keys(), ...fetchedOffline, ...viewing.keys()]))
}

export function reloadOfflineLogs(): Set<number> {
  const ids = new Set(fetchedOffline)
  staleLogs(ids)
  return ids
}

export function ensureLog(id: number): Promise<unknown> {
  return logLoaded.has(id) ? Promise.resolve() : loadOperationLog(id)
}

export function viewOp(id: number) {
  viewing.set(id, (viewing.get(id) ?? 0) + 1)
  ensureLog(id).catch(() => {})
  return () => {
    const n = (viewing.get(id) ?? 1) - 1
    if (n > 0) viewing.set(id, n); else viewing.delete(id)
  }
}
