import { getToken, type Message } from './api'
import { editMap, setIn } from './maps'
import {
  applyRuns, bumpAllRefreshes, bumpRefresh, connected, daemon, health, loadApplyRun, loadMachines, loadPlans, loadVersions, machineKey, machines, plans,
  reconnectAttempt, reloadClusters, resyncing, statuses, stopped, toast, upsertCluster,
} from './store'

let ws: WebSocket | null = null
let lastSeq = 0
let helloSeq = 0
let startedAt = ''
let attempt = 0
let everConnected = false
let resyncedAtHello = false
let resyncWanted = 0
let resyncDone = 0
let resyncRun: Promise<void> | null = null

export function connectLive() {
  if (ws) return
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


function resync() {
  resyncWanted++
  resyncRun ??= runResync().finally(() => { resyncRun = null })
}

async function runResync() {
  resyncing.value = true
  try {
    while (resyncDone < resyncWanted) {
      resyncDone = resyncWanted
      await Promise.all([reloadClusters(), loadMachines(), loadVersions(), loadPlans()])
      bumpAllRefreshes()
    }
  } finally {
    resyncing.value = false
  }
}

function hello(m: Message) {
  connected.value = true
  stopped.value = false
  attempt = 0
  reconnectAttempt.value = 0
  const h = m.hello
  if (!h) return
  daemon.value = { version: h.version, startedAt: h.startedAt, os: h.os }
  const restarted = h.seq < lastSeq || (!!startedAt && h.startedAt !== startedAt)
  startedAt = h.startedAt
  helloSeq = h.seq
  resyncedAtHello = !everConnected || restarted
  if (resyncedAtHello) { everConnected = true; lastSeq = helloSeq; resync() }
}

function apply(m: Message) {
  if (m.kind === 'hello') return hello(m)
  if (m.kind === 'stopped') { stopped.value = true; return }
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
    case 'machine':
      if (m.machine) setIn(machines, machineKey(m.machine), m.machine)
      break
    case 'versions':
      loadVersions()
      break
    case 'status':
      if (m.status && m.cluster) setIn(statuses, m.cluster, m.status)
      break
    case 'health':
      if (m.health) {
        const h = m.health
        editMap(health, (hm) => {
          const list = hm.get(h.cluster) ?? []
          hm.set(h.cluster, list.some((e) => e.id === h.id) ? list.map((e) => e.id === h.id ? h : e) : [h, ...list].slice(0, 200))
        })
        if (h.open && !replayed) toast(`${h.cluster}: ${h.message}`, 'error')
      }
      break
    case 'apply': {
      const c = m.cluster ?? '', line = m.line
      if (line) editMap(applyRuns, (rm) => { const r = rm.get(c) ?? { running: true, lines: [] }; rm.set(c, { ...r, running: true, lines: [...r.lines, line] }) })
      break
    }
    case 'plan':
      if (m.plan && m.cluster) setIn(plans, m.cluster, m.plan)
      break
    case 'refresh':
      if (m.scope === 'machines') loadMachines()
      if (m.scope === 'apply' && m.cluster) loadApplyRun(m.cluster)
      bumpRefresh(m.cluster ?? '', m.scope ?? '')
      break
  }
}
