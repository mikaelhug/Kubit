import { fmt, getToken, type Message } from './api'
import { editMap, setIn } from './maps'
import { pushEvent, reloadLogs, reloadOfflineLogs, reloadOperations, operations, upsertOp } from './ops'
import {
  audit, bumpAllRefreshes, bumpRefresh, clusters, connected, daemon, health, hostSamples, loadAllHealth, loadMachines, loadObserver, loadSettings, loadSnapshots, loadVersions, machineKey, machines, me, observer,
  reconnectAttempt, reloadClusters, resyncing, settings, snapshots, statuses, toast, upsertCluster,
} from './store'

let ws: WebSocket | null = null
let lastSeq = 0
let helloSeq = 0
let startedAt = ''
let attempt = 0
let everConnected = false
let resyncedAtHello = false
let replaySkip = new Set<number>()
let resyncWanted = 0
let resyncDone = 0
let resyncRun: Promise<void> | null = null

export function connectLive() {
  if (ws || me.value === null) return
  const t = getToken()
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  const q = new URLSearchParams()
  if (t) q.set('token', t)
  if (lastSeq) q.set('since', String(lastSeq))
  const socket = new WebSocket(`${proto}://${location.host}/api/v1/ws?${q}`)
  ws = socket
  socket.onmessage = (ev) => { if (ws === socket) apply(JSON.parse(ev.data) as Message) }
  socket.onclose = () => {
    if (ws !== socket) return
    ws = null
    connected.value = false
    attempt++
    reconnectAttempt.value = attempt
    setTimeout(connectLive, Math.min(30000, 1000 * 2 ** Math.min(attempt, 5)))
  }
  socket.onerror = () => socket.close()
}

function resume() {
  const old = ws
  ws = null
  old?.close()
  connectLive()
}

export function reconnectLive() {
  everConnected = false
  lastSeq = 0
  attempt = 0
  startedAt = ''
  resume()
}

function resync() {
  resyncWanted++
  resyncRun ??= runResync().finally(() => { resyncRun = null })
}

async function runResync() {
  resyncing.value = true
  try {
    while (resyncDone < resyncWanted) {
      resyncDone = resyncWanted
      await Promise.all([
        reloadClusters(), reloadOperations(), reloadLogs(), loadMachines(), loadSettings(), loadObserver(), loadVersions(),
        loadAllHealth([...health.value.keys()]), ...[...snapshots.value.keys()].map(loadSnapshots),
      ])
      bumpAllRefreshes()
    }
  } finally {
    resyncing.value = false
  }
}

function hello(m: Message) {
  connected.value = true
  attempt = 0
  reconnectAttempt.value = 0
  const h = m.hello
  if (!h) return
  daemon.value = { version: h.version, startedAt: h.startedAt, service: h.service, os: h.os }
  const restarted = h.seq < lastSeq || (!!startedAt && h.startedAt !== startedAt)
  startedAt = h.startedAt
  helloSeq = h.seq
  resyncedAtHello = !everConnected || restarted
  replaySkip = resyncedAtHello ? new Set() : reloadOfflineLogs()
  if (resyncedAtHello) { everConnected = true; lastSeq = helloSeq; resync() }
}

function apply(m: Message) {
  if (m.kind === 'hello') return hello(m)
  if (m.seq) {
    if (m.seq <= lastSeq) return
    if (m.seq > lastSeq + 1) return resume()
    lastSeq = m.seq
  } else if (m.kind === 'resync') lastSeq = Math.max(lastSeq, helloSeq)
  const replayed = !!m.seq && m.seq <= helloSeq
  switch (m.kind) {
    case 'resync':
      if (m.seq || !resyncedAtHello) resync()
      break
    case 'cluster':
      if (m.clusterRow) upsertCluster(m.clusterRow)
      break
    case 'clusterRemoved': {
      const key = m.key ?? ''
      clusters.value = clusters.value.filter((c) => c.name !== key)
      editMap(health, (hm) => hm.delete(key))
      editMap(statuses, (sm) => sm.delete(key))
      editMap(snapshots, (sm) => sm.delete(key))
      break
    }
    case 'machine':
      if (m.machine) setIn(machines, machineKey(m.machine), m.machine)
      break
    case 'machineRemoved':
      editMap(machines, (mm) => { for (const [k, v] of mm) if (k === m.key || v.ip === m.key) mm.delete(k) })
      break
    case 'snapshot': {
      const snap = m.snapshot
      const cluster = m.cluster
      if (!snap || !cluster) break
      editMap(snapshots, (sm) => sm.set(cluster, [snap, ...(sm.get(cluster) ?? []).filter((s) => s.id !== snap.id)].sort((a, b) => b.id - a.id)))
      break
    }
    case 'snapshotRemoved':
      editMap(snapshots, (sm) => { for (const [c, list] of sm) sm.set(c, list.filter((s) => String(s.id) !== m.key)) })
      break
    case 'audit':
      if (m.audit && !audit.value.some((a) => a.id === m.audit!.id)) audit.value = [m.audit, ...audit.value].slice(0, 500)
      break
    case 'settings':
      if (m.settings) settings.value = m.settings
      break
    case 'versions':
      loadVersions()
      break
    case 'operation':
      if (m.operation) {
        const prev = operations.value.get(m.operation.id)
        upsertOp(m.operation)
        if (m.operation.status !== 'running' && prev?.status === 'running' && !replayed) {
          toast(`${fmt.kind(m.operation.kind)}${m.operation.cluster ? ' · ' + m.operation.cluster : ''}: ${m.operation.status}`, m.operation.status === 'done' ? 'good' : 'error')
        }
      }
      break
    case 'event':
      if (m.event && m.operationId !== undefined && !(replayed && replaySkip.has(m.operationId))) pushEvent(m.operationId, m.event)
      break
    case 'status':
      if (m.status && m.cluster) setIn(statuses, m.cluster, m.status)
      break
    case 'health':
      if (m.health) {
        const h = m.health
        editMap(health, (hm) => hm.set(h.cluster, [h, ...(hm.get(h.cluster) ?? []).filter((e) => e.id !== h.id)].slice(0, 200)))
        if (h.severity !== 'info' && !h.acked && !replayed) toast(h.cluster.startsWith('labhost:') ? h.message : `${h.cluster}: ${h.message}`, 'error')
      }
      break
    case 'observer':
      if (m.observer) observer.value = m.observer
      break
    case 'hostSample':
      if (m.sample && m.key) setIn(hostSamples, m.key, m.sample)
      break
    case 'healthAck': {
      const c = m.cluster ?? ''
      editMap(health, (hm) => hm.set(c, (hm.get(c) ?? []).map((e) => m.key === '*' || String(e.id) === m.key ? { ...e, acked: true } : e)))
      break
    }
    case 'healthResolved': {
      const c = m.cluster ?? ''
      editMap(health, (hm) => hm.set(c, (hm.get(c) ?? []).map((e) => e.kind === m.key && (e.node ?? '') === (m.node ?? '') ? { ...e, acked: true } : e)))
      break
    }
    case 'refresh':
      if (m.scope === 'machines') loadMachines()
      bumpRefresh(m.cluster ?? '', m.scope ?? '')
      break
  }
}
