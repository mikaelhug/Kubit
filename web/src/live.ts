import { fmt, getToken, type Message } from './api'
import { editMap, setIn } from './maps'
import {
  applyStepEvent, audit, bumpAllRefreshes, bumpRefresh, clusters, connected, daemon, health, hostSamples, loadMachines, loadObserver, loadSettings, loadVersions, machineKey, machines, me, nextEventSeq, observer, opEvents, operations,
  reconnectAttempt, reloadClusters, reloadOperations, resyncing, settings, snapshots, statuses, toast, upsertCluster, upsertOp,
} from './store'

let ws: WebSocket | null = null
let lastSeq = 0
let attempt = 0
let everConnected = false

export function connectLive() {
  if (ws || me.value === null) return
  const t = getToken()
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  const q = new URLSearchParams()
  if (t) q.set('token', t)
  if (lastSeq) q.set('since', String(lastSeq))
  ws = new WebSocket(`${proto}://${location.host}/api/v1/ws?${q}`)
  ws.onmessage = (ev) => apply(JSON.parse(ev.data) as Message)
  ws.onclose = () => {
    ws = null
    connected.value = false
    attempt++
    reconnectAttempt.value = attempt
    setTimeout(connectLive, Math.min(30000, 1000 * 2 ** Math.min(attempt, 5)))
  }
  ws.onerror = () => ws?.close()
}

export function reconnectLive() {
  everConnected = false
  lastSeq = 0
  attempt = 0
  const old = ws
  ws = null
  if (old) { old.onclose = null; old.close() }
  connectLive()
}

async function resync() {
  resyncing.value = true
  try {
    await Promise.all([reloadClusters(), reloadOperations(), loadMachines(), loadSettings(), loadObserver(), loadVersions()])
    bumpAllRefreshes()
  } finally {
    resyncing.value = false
  }
}

let helloSeq = 0

function apply(m: Message) {
  if (m.seq) lastSeq = m.seq
  const replayed = !!m.seq && m.seq <= helloSeq
  switch (m.kind) {
    case 'hello': {
      connected.value = true
      attempt = 0
      reconnectAttempt.value = 0
      if (m.hello) daemon.value = { version: m.hello.version, startedAt: m.hello.startedAt, service: m.hello.service, os: m.hello.os }
      const restarted = m.hello && m.hello.seq < lastSeq
      helloSeq = m.hello?.seq ?? 0
      if (!everConnected || restarted) { everConnected = true; lastSeq = helloSeq; resync() }
      break
    }
    case 'resync':
      resync()
      break
    case 'cluster':
      if (m.clusterRow) upsertCluster(m.clusterRow)
      break
    case 'clusterRemoved':
      clusters.value = clusters.value.filter((c) => c.name !== m.key)
      editMap(health, (hm) => hm.delete(m.key ?? ''))
      break
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
        if (m.operation.status !== 'running' && prev?.status === 'running') {
          if (!replayed) toast(`${fmt.kind(m.operation.kind)}${m.operation.cluster ? ' · ' + m.operation.cluster : ''}: ${m.operation.status}`, m.operation.status === 'done' ? 'good' : 'error')
        }
      }
      break
    case 'event':
      if (m.event && m.operationId !== undefined) {
        const id = m.operationId
        const e = { ...m.event, seq: nextEventSeq() }
        if (e.kind === 'log' || !e.kind) {
          editMap(opEvents, (map) => {
            const list = map.get(id) ?? []
            map.set(id, list.length > 2000 ? [...list.slice(-1500), e] : [...list, e])
          })
        }
        applyStepEvent(id, e)
      }
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
      bumpRefresh(m.cluster ?? '', m.scope ?? '')
      break
  }
}
