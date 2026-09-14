import { useEffect, useState } from 'preact/hooks'
import { api, fmt, type HealthEvent, type Sample } from '../../api'
import { ack, health, operations } from '../../store'
import { Sparkline } from '../../components/Sparkline'
import { Notice, Pill, Section, StatusDot } from '../../components/ui'
import type { ClusterCtx } from './ClusterPage'

export function Overview({ ctx }: { ctx: ClusterCtx }) {
  const { status, cluster, name } = ctx
  const t = status?.totals
  const spec = cluster.spec.spec
  const recent = [...operations.value.values()].filter((o) => o.cluster === name).sort((a, b) => b.id - a.id).slice(0, 5)
  const cpDown = status ? status.nodes.filter((n) => n.role === 'controlplane' && (!n.talosReachable || !n.ready)).length : 0
  const events = health.value.get(name) ?? []
  const alerts = events.filter((e) => !e.acked && e.severity !== 'info')
  const [range, setRange] = useState('24h')
  const [samples, setSamples] = useState<Sample[]>([])
  useEffect(() => { api.samples(name, range).then(setSamples).catch(() => {}) }, [name, range, status?.totals.pods])
  const pts = (f: (s: Sample) => number) => samples.map((s) => ({ t: new Date(s.ts).getTime(), v: f(s) }))
  const last = samples[samples.length - 1]

  return (
    <>
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
      <div class="grid grid-cols-2 xl:grid-cols-4 gap-4">
        <Card label="Control plane" tone={!status ? 'muted' : cpDown === 0 ? 'good' : 'bad'} value={status ? `${status.nodes.filter((n) => n.role === 'controlplane').length - cpDown}/${status.nodes.filter((n) => n.role === 'controlplane').length}` : '—'} sub={spec.controlPlane.vip ? `VIP ${spec.controlPlane.vip}` : 'no VIP: endpoint is the first control plane'} />
        <Card label="etcd quorum" tone={!status ? 'muted' : status.etcd.healthy ? 'good' : 'bad'} value={status ? `${status.etcd.members}/${status.etcd.expected}` : '—'} sub={status?.etcd.leader ? `leader ${status.etcd.leader}` : status?.etcd.alarms?.join(', ') || 'no leader reported'} />
        <Card label="Nodes Ready" tone={!t ? 'muted' : t.nodesReady === t.nodes ? 'good' : 'warn'} value={t ? `${t.nodesReady}/${t.nodes}` : '—'} sub={`${spec.nodes.filter((n) => n.role === 'worker').length} worker${spec.nodes.filter((n) => n.role === 'worker').length === 1 ? '' : 's'}`} />
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
          <Section title="Recent events" help="State changes the watcher observed: reachability, readiness, etcd membership, versions.">
            <div class="panel divide-y divide-border/60 max-h-[260px] overflow-auto">
              {events.length === 0 && <div class="p-4 text-[13px] text-muted">Nothing observed yet.</div>}
              {events.slice(0, 30).map((e) => <EventRow key={e.id} e={e} />)}
            </div>
          </Section>
          <Section title="Recent operations">
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

function EventRow({ e, onAck }: { e: HealthEvent; onAck?: () => void }) {
  const tone = e.severity === 'critical' ? 'bad' : e.severity === 'warn' ? 'warn' : 'good'
  return (
    <div class="flex items-center gap-3 px-4 py-2 text-[13px]">
      <StatusDot tone={tone} />
      <span class="num text-muted text-[12px] whitespace-nowrap shrink-0">{fmt.when(e.ts)}</span>
      <span class={`min-w-0 truncate ${e.acked ? 'text-muted' : ''}`} title={e.message}>{e.message}</span>
      <span class="ml-auto mono text-[11px] text-muted">{e.kind}</span>
      {onAck && <button class="btn !py-0.5 !px-2 text-[11px]" onClick={onAck}>Ack</button>}
    </div>
  )
}

function Card({ label, value, tone, sub }: { label: string; value: string; tone: 'good' | 'warn' | 'bad' | 'muted'; sub?: string }) {
  const color = { good: 'text-good', warn: 'text-warn', bad: 'text-bad', muted: 'text-muted' }[tone]
  return (
    <div class="panel p-4 flex flex-col gap-1 min-w-0">
      <span class="label">{label}</span>
      <span class={`text-2xl font-semibold num truncate ${color}`}>{value}</span>
      {sub && <span class="text-[12px] text-muted truncate" title={sub}>{sub}</span>}
    </div>
  )
}
