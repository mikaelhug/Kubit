// The one live connection. Every message the daemon publishes lands here and is
// applied to the signals in store.ts; a reconnect replays what was missed (?since)
// or, when the daemon says so, reloads the base state once.
import { fmt, getToken, type Message } from './api'
import {
  applyStepEvent, audit, clusters, connected, daemon, health, hostSamples, latestTalos, loadMachines, loadObserver, loadSettings, machines, me, observer, opEvents, operations,
  reconnectAttempt, refreshes, reloadClusters, reloadOperations, resyncing, settings, snapshots, statuses, toast, upsertCluster, upsertOp,
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

/** After sign-in or sign-out: drop the socket and start over with the new identity. */
export function reconnectLive() {
  everConnected = false
  lastSeq = 0
  attempt = 0
  const old = ws
  ws = null
  if (old) { old.onclose = null; old.close() }
  connectLive()
}

/** Reload everything the console derives from; views refetch through bumped scopes. */
export async function resync() {
  resyncing.value = true
  try {
    await Promise.all([reloadClusters(), reloadOperations(), loadMachines(), loadSettings(), loadObserver()])
    const rm = new Map(refreshes.value)
    for (const k of rm.keys()) rm.set(k, (rm.get(k) ?? 0) + 1)
    rm.set('*/resync', (rm.get('*/resync') ?? 0) + 1)
    refreshes.value = rm
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
      // First connection, or a daemon restart (sequence went backwards): full load.
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
    case 'clusterRemoved': {
      clusters.value = clusters.value.filter((c) => c.name !== m.key)
      const hm = new Map(health.value)
      hm.delete(m.key ?? '')
      health.value = hm
      break
    }
    case 'machine':
      if (m.machine) {
        const mm = new Map(machines.value)
        mm.set(m.machine.mac || `ip:${m.machine.ip}`, m.machine)
        machines.value = mm
      }
      break
    case 'machineRemoved': {
      const mm = new Map(machines.value)
      for (const [k, v] of mm) if (k === m.key || v.ip === m.key) mm.delete(k)
      machines.value = mm
      break
    }
    case 'snapshot': {
      if (!m.snapshot || !m.cluster) break
      const sm = new Map(snapshots.value)
      const list = (sm.get(m.cluster) ?? []).filter((s) => s.id !== m.snapshot!.id)
      sm.set(m.cluster, [m.snapshot, ...list].sort((a, b) => b.id - a.id))
      snapshots.value = sm
      break
    }
    case 'snapshotRemoved': {
      const sm = new Map(snapshots.value)
      for (const [c, list] of sm) sm.set(c, list.filter((s) => String(s.id) !== m.key))
      snapshots.value = sm
      break
    }
    case 'audit':
      if (m.audit && !audit.value.some((a) => a.id === m.audit!.id)) audit.value = [m.audit, ...audit.value].slice(0, 500)
      break
    case 'settings':
      if (m.settings) settings.value = m.settings
      break
    case 'versions':
      latestTalos.value = m.key ?? ''
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
        const e = m.event
        if (e.kind === 'log' || !e.kind) {
          const map = new Map(opEvents.value)
          const list = map.get(m.operationId) ?? []
          map.set(m.operationId, list.length > 2000 ? [...list.slice(-1500), e] : [...list, e])
          opEvents.value = map
        }
        applyStepEvent(m.operationId, e)
      }
      break
    case 'status':
      if (m.status && m.cluster) {
        const sm = new Map(statuses.value)
        sm.set(m.cluster, m.status)
        statuses.value = sm
      }
      break
    case 'health':
      if (m.health) {
        const hm = new Map(health.value)
        const h = m.health
        hm.set(h.cluster, [h, ...(hm.get(h.cluster) ?? []).filter((e) => e.id !== h.id)].slice(0, 200))
        health.value = hm
        if (h.severity !== 'info' && !h.acked && !replayed) toast(h.cluster.startsWith('labhost:') ? h.message : `${h.cluster}: ${h.message}`, 'error')
      }
      break
    case 'observer':
      if (m.observer) observer.value = m.observer
      break
    case 'hostSample':
      if (m.sample && m.key) hostSamples.value = new Map(hostSamples.value).set(m.key, m.sample)
      break
    case 'healthAck': {
      // The daemon is the authority on acks: same view in every tab.
      const hm = new Map(health.value)
      const c = m.cluster ?? ''
      hm.set(c, (hm.get(c) ?? []).map((e) => m.key === '*' || String(e.id) === m.key ? { ...e, acked: true } : e))
      health.value = hm
      break
    }
    case 'healthResolved': {
      const hm = new Map(health.value)
      const c = m.cluster ?? ''
      hm.set(c, (hm.get(c) ?? []).map((e) => e.kind === m.key && (e.node ?? '') === (m.node ?? '') ? { ...e, acked: true } : e))
      health.value = hm
      break
    }
    case 'refresh': {
      const rm = new Map(refreshes.value)
      const k = `${m.cluster ?? ''}/${m.scope}`
      rm.set(k, (rm.get(k) ?? 0) + 1)
      refreshes.value = rm
      break
    }
  }
}
