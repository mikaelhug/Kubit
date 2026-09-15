import { useEffect, useState } from 'preact/hooks'
import { api, fmt, type HealthEvent, type Sample, type Versions } from '../../api'
import { ack, health, latestTalos as latestTalosSignal, operations, statuses } from '../../store'
import { runbookFor } from '../../runbooks'
import { Sparkline } from '../../components/Sparkline'
import { Notice, Pill, Section, StatusDot } from '../../components/ui'
import type { ClusterCtx } from './ClusterPage'

const recoveryKinds = new Set(['talos.back', 'node.ready', 'api.back', 'etcd.healthy', 'lb.assigned', 'workload.available', 'pod.recovered', 'pvc.bound', 'service.endpoints', 'ingress.address', 'lb.pool-free'])

export function Overview({ ctx }: { ctx: ClusterCtx }) {
  const { status, cluster, name } = ctx
  const t = status?.totals
  const spec = cluster.spec.spec
  const recent = [...operations.value.values()].filter((o) => o.cluster === name).sort((a, b) => b.id - a.id).slice(0, 5)
  const cpDown = status ? status.nodes.filter((n) => n.role === 'controlplane' && (!n.talosReachable || !n.ready)).length : 0
  const events = health.value.get(name) ?? []
  const alerts = events.filter((e) => !e.acked && e.severity !== 'info')
  // Info events earn a place only when they close an alert.
  const notable = events.filter((e) => e.severity !== 'info' || recoveryKinds.has(e.kind))
  const [range, setRange] = useState('24h')
  const [samples, setSamples] = useState<Sample[]>([])
  const [versions, setVersions] = useState<Versions | null>(null)
  useEffect(() => { api.versions().then(setVersions).catch(() => {}) }, [name, latestTalosSignal.value])
  const latestTalos = versions?.talos.find((v) => !v.includes('-'))
  const updates = [
    latestTalos && verLess(spec.talosVersion, latestTalos) ? `Talos ${latestTalos} (running ${spec.talosVersion})` : '',
    versions && verLess(spec.kubernetesVersion, versions.kubernetesLatest) ? `Kubernetes ${versions.kubernetesLatest} (running ${spec.kubernetesVersion})` : '',
  ].filter(Boolean)
  useEffect(() => { api.samples(name, range).then(setSamples).catch(() => {}) }, [name, range])
  // Every pushed status is also the newest sample: append it so the graphs move
  // without refetching.
  useEffect(() => {
    if (!status?.observedAt || !t) return
    setSamples((prev) => {
      const last = prev[prev.length - 1]
      if (last && last.ts >= status.observedAt!) return prev
      const point: Sample = { ts: status.observedAt!, cpuMilli: t.cpuMilli, cpuCap: t.cpuCapMilli, memBytes: t.memBytes, memCap: t.memCapBytes, pods: t.pods, ready: t.nodesReady === t.nodes, reachable: status.apiReachable }
      return [...prev, point]
    })
  }, [status?.observedAt]) // eslint-disable-line
  const pts = (f: (s: Sample) => number) => samples.map((s) => ({ t: new Date(s.ts).getTime(), v: f(s) }))
  const last = samples[samples.length - 1]

  return (
    <>
      {updates.length > 0 && <Notice tone="info"><span class="flex items-center gap-2">Update available: {updates.join(' · ')}<a href={`/clusters/${name}/settings`} class="ml-auto text-accent hover:underline text-[12px]">Versions →</a></span></Notice>}
      {status?.apiError && <Notice tone="warn">Kubernetes API unreachable at {status.endpoint}: {status.apiError}. Readiness and usage come from the last known state.</Notice>}
      {alerts.length > 0 && (
        <div class="panel border-warn/50">
          <div class="flex items-center gap-2 px-4 py-2 border-b border-border">
            <span class="font-semibold">{alerts.length} active alert{alerts.length === 1 ? '' : 's'}</span>
            <span class="text-[12px] text-muted">from the health watcher; acknowledge once handled</span>
            <button class="btn !py-0.5 !px-2 text-[12px] ml-auto" onClick={() => ack(name)}>Acknowledge all</button>
          </div>
          {alerts.map((e) => <EventRow key={e.id} e={e} onAck={() => ack(name, e.id)} />)}
        </div>
      )}
      <div class="grid grid-cols-2 xl:grid-cols-5 gap-4">
        <Card label="Control plane" tone={!status ? 'muted' : cpDown === 0 ? 'good' : 'bad'} value={status ? `${status.nodes.filter((n) => n.role === 'controlplane').length - cpDown}/${status.nodes.filter((n) => n.role === 'controlplane').length}` : '—'} sub={spec.controlPlane.vip ? `VIP ${spec.controlPlane.vip}` : 'no VIP: endpoint is the first control plane'} />
        <Card label="etcd quorum" tone={!status ? 'muted' : status.etcd.healthy ? 'good' : 'bad'} value={status ? `${status.etcd.members}/${status.etcd.expected}` : '—'} sub={status?.etcd.leader ? `leader ${status.etcd.leader}` : status?.etcd.alarms?.join(', ') || 'no leader reported'} />
        <Card label="Nodes Ready" tone={!t ? 'muted' : t.nodesReady === t.nodes ? 'good' : 'warn'} value={t ? `${t.nodesReady}/${t.nodes}` : '—'} sub={`${spec.nodes.filter((n) => n.role === 'worker').length} worker${spec.nodes.filter((n) => n.role === 'worker').length === 1 ? '' : 's'}`} />
        <ObserverCard status={status} />
        <Card label="Load balancer" tone={status?.platform?.outputs?.ingress_ip ? 'good' : spec.platform.metallb.enabled ? 'warn' : 'muted'} value={status?.platform?.outputs?.ingress_ip ?? (spec.platform.metallb.enabled ? 'pending' : 'off')} sub={spec.platform.metallb.enabled ? `pool ${spec.platform.metallb.range}` : 'MetalLB disabled'} />
      </div>
      <div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <div class="panel p-4 flex flex-col gap-4">
          <div class="flex items-center gap-2">
            <span class="label">Capacity trend</span>
            <div class="ml-auto flex gap-1">
              {['1h', '6h', '24h', '7d'].map((r) => <button key={r} class={`btn !py-0.5 !px-2 text-[11px] ${r === range ? 'border-accent text-accent' : ''}`} onClick={() => setRange(r)}>{r}</button>)}
            </div>
          </div>
          <Sparkline label="CPU used" points={pts((s) => s.cpuMilli)} max={last?.cpuCap} format={fmt.cores} />
          <Sparkline label="Memory used" points={pts((s) => s.memBytes)} max={last?.memCap} format={fmt.bytes} />
          <Sparkline label="Pods" points={pts((s) => s.pods)} max={t?.podCap} format={String} />
          <span class="text-[11px] text-muted">Sampled every 15 s by the daemon (kept 24 h, then hourly for 30 d). Gaps mean the API was unreachable.</span>
        </div>
        <div class="flex flex-col gap-4">
          <Section title="Recent events" help="Alerts the watcher raised and their recoveries; routine state changes (node Ready, API back, version notes) are not shown.">
            <div class="panel divide-y divide-border/60 max-h-[260px] overflow-auto">
              {notable.length === 0 && <div class="p-4 text-[13px] text-muted">Nothing worth reporting.</div>}
              {notable.slice(0, 30).map((e) => <EventRow key={e.id} e={e} />)}
            </div>
          </Section>
          <Section title="Recent operations" actions={<a href={`/operations?cluster=${name}`} class="text-[12px] text-accent hover:underline">All activity →</a>}>
            <div class="panel divide-y divide-border/60">
              {recent.length === 0 && <div class="p-4 text-[13px] text-muted">Nothing yet.</div>}
              {recent.map((o) => (
                <a key={o.id} href={`/operations/${o.id}`} class="flex items-center gap-3 px-4 py-2 text-[13px] hover:bg-panel-2">
                  <StatusDot tone={o.status === 'done' ? 'good' : o.status === 'running' ? 'warn' : o.status === 'failed' ? 'bad' : 'muted'} pulse={o.status === 'running'} />
                  <span class="font-medium">{fmt.kind(o.kind)}</span>
                  <span class="text-muted">#{o.id}</span>
                  <span class="ml-auto text-muted num">{fmt.datetime(o.startedAt)}</span>
                  <Pill tone={o.status === 'done' ? 'good' : o.status === 'running' ? 'warn' : o.status === 'failed' ? 'bad' : 'muted'}>{o.status}</Pill>
                </a>
              ))}
            </div>
          </Section>
        </div>
      </div>
    </>
  )
}

export function EventRow({ e, onAck }: { e: HealthEvent; onAck?: () => void }) {
  const tone = e.severity === 'critical' ? 'bad' : e.severity === 'warn' ? 'warn' : 'good'
  const [open, setOpen] = useState(false)
  const st = statuses.value.get(e.cluster)
  const ip = e.node ? st?.nodes.find((n) => n.hostname === e.node)?.ip : undefined
  const rb = onAck ? runbookFor(e.kind, { cluster: e.cluster, node: e.node, nodeHref: ip ? `/nodes/${ip}` : undefined }) : null
  return (
    <div class="flex flex-col">
      <div class="flex items-center gap-3 px-4 py-2 text-[13px]">
        <StatusDot tone={tone} />
        <span class="num text-muted text-[12px] whitespace-nowrap shrink-0">{fmt.when(e.ts)}</span>
        <span class={`min-w-0 truncate ${e.acked ? 'text-muted' : ''}`} title={e.message}>{e.message}</span>
        {objectLink(e) && <a href={objectLink(e)!} class="text-[11px] text-accent hover:underline shrink-0">open</a>}
        <span class="ml-auto mono text-[11px] text-muted">{e.kind}</span>
        {rb && <button class={`btn !py-0.5 !px-2 text-[11px] ${open ? 'border-accent' : ''}`} onClick={() => setOpen(!open)}>{open ? 'Hide' : 'What to do'}</button>}
        {onAck && <button class="btn !py-0.5 !px-2 text-[11px]" onClick={onAck}>Ack</button>}
      </div>
      {open && rb && (
        <div class="mx-4 mb-3 rounded-md border border-border bg-panel-2/60 px-4 py-3 text-[13px] flex flex-col gap-2">
          <div><span class="font-medium">{rb.title}</span> <span class="text-muted">— {rb.why}</span></div>
          <ol class="list-decimal pl-5 flex flex-col gap-1">
            {rb.steps.map((s, i) => <li key={i}>{s.text} {s.link && <a href={s.link.href} class="text-accent hover:underline whitespace-nowrap">{s.link.label} →</a>}</li>)}
          </ol>
        </div>
      )}
    </div>
  )
}

/** How current the watcher's view is, and whether scheduled snapshots are keeping up. */
function ObserverCard({ status }: { status?: { observedAt?: string; lastSnapshotAt?: string; snapshotInterval?: string } | null }) {
  const [, tick] = useState(0)
  useEffect(() => { const t = setInterval(() => tick((n) => n + 1), 15000); return () => clearInterval(t) }, [])
  if (!status?.observedAt) return <Card label="Observer" tone="muted" value="—" sub="no status from the watcher yet" />
  const seenAgo = (Date.now() - new Date(status.observedAt).getTime()) / 1000
  const stale = seenAgo > 120
  const interval = parseDuration(status.snapshotInterval ?? '6h')
  const snapAgo = status.lastSnapshotAt ? (Date.now() - new Date(status.lastSnapshotAt).getTime()) / 1000 : null
  const snapLate = interval > 0 && (snapAgo === null || snapAgo > 2 * interval)
  const sub = interval === 0 ? 'etcd snapshots: schedule off' : snapAgo === null ? 'no etcd snapshot yet' : `last etcd snapshot ${fmt.when(status.lastSnapshotAt!)}`
  return <Card label="Observer" tone={stale ? 'warn' : snapLate ? 'warn' : 'good'} value={stale ? `${Math.round(seenAgo / 60)} min ago` : 'live'} sub={stale ? `watcher last saw this cluster ${fmt.when(status.observedAt)}; ${sub}` : sub} />
}

/** Semver-ish compare on the numeric part; prereleases never count as newer. */
function verLess(a: string, b: string): boolean {
  if (b.includes('-')) return false
  const pa = a.replace(/^v/, '').split('-')[0].split('.').map(Number), pb = b.replace(/^v/, '').split('.').map(Number)
  for (let i = 0; i < 3; i++) { if ((pa[i] ?? 0) !== (pb[i] ?? 0)) return (pa[i] ?? 0) < (pb[i] ?? 0) }
  return false
}

function parseDuration(s: string): number {
  const m = /^(\d+(?:\.\d+)?)(h|m|s)$/.exec(s.trim())
  if (!m) return s === '0' ? 0 : 6 * 3600
  return Number(m[1]) * ({ h: 3600, m: 60, s: 1 }[m[2]] ?? 1)
}

/** Workload alerts carry the object as kind/namespace/name; link to the page that shows it. */
function objectLink(e: HealthEvent): string | null {
  const m = /^(\w+)\/([^/]+)\/(.+)$/.exec(e.node ?? '')
  if (!m) return null
  const [, kind, ns] = m
  const page = kind === 'PersistentVolumeClaim' ? 'storage' : kind === 'Service' || kind === 'Ingress' || kind === 'MetalLB' ? 'network' : 'workloads'
  return `/clusters/${e.cluster}/${page}?ns=${encodeURIComponent(ns)}`
}

export function Card({ label, value, tone, sub }: { label: string; value: string; tone: 'good' | 'warn' | 'bad' | 'muted'; sub?: string }) {
  const color = { good: 'text-good', warn: 'text-warn', bad: 'text-bad', muted: 'text-muted' }[tone]
  return (
    <div class="panel p-4 flex flex-col gap-1 min-w-0">
      <span class="label">{label}</span>
      <span class={`text-2xl font-semibold num truncate ${color}`}>{value}</span>
      {sub && <span class="text-[12px] text-muted truncate" title={sub}>{sub}</span>}
    </div>
  )
}
