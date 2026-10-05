import { useEffect, useMemo, useState } from 'preact/hooks'
import { api, fmt, type Sample, type ServiceHealth, type Status } from '../../api'
import { nowEvery } from '../../clock'
import { AlertGroup, EventRow } from '../../components/Alerts'
import { behindText, useConfigStatus } from '../../configStatus'
import { useNamespaces } from '../../components/NamespaceScope'
import { RangeButtons, Sparkline, spanOf } from '../../components/Sparkline'
import { Ago } from '../../components/Time'
import { Notice, Pill, Section, SeenAgo, StatusDot, Tile } from '../../components/ui'
import { opsFor } from '../../ops'
import { health, loadSnapshots, openAlerts, snapshots, versions } from '../../store'
import { appendWithin } from '../../time'
import { stateTone, type Tone } from '../../tone'
import { useLive } from '../../useLive'
import { updatesFor } from '../../versions'
import type { ClusterCtx } from './ClusterPage'

const recoveryKinds = new Set(['talos.back', 'node.ready', 'api.back', 'etcd.healthy', 'lb.assigned', 'workload.available', 'pod.recovered', 'pvc.bound', 'service.endpoints', 'ingress.address', 'lb.pool-free'])

export function Overview({ ctx }: { ctx: ClusterCtx }) {
  const { status, cluster, name } = ctx
  const t = status?.totals
  const spec = cluster.spec.spec
  const cps = status ? status.nodes.filter((n) => n.role === 'controlplane') : []
  const cpDown = cps.filter((n) => !n.talosReachable || !n.ready).length
  const events = health.value.get(name) ?? []
  const notable = events.filter((e) => e.severity !== 'info' || recoveryKinds.has(e.kind))
  const u = updatesFor(spec.talosVersion, spec.kubernetesVersion, versions.value)
  const updates = [u.talos ? `Talos ${u.talos} (running ${spec.talosVersion})` : '', u.kubernetes ? `Kubernetes ${u.kubernetes} (running ${spec.kubernetesVersion})` : ''].filter(Boolean)
  const { data: service } = useLive(() => api.serviceHealth(name).then((r) => r.latest), [name], [[name, 'services']], { onError: 'silent' })
  const down = !!status && !status.apiReachable
  const workers = spec.nodes.filter((n) => n.role === 'worker').length
  const behind = useConfigStatus(name)?.behind ?? []

  return (
    <>
      {updates.length > 0 && <Notice tone="info">Update available: {updates.join(' · ')}</Notice>}
      {behind.length > 0 && <Notice tone="warn"><span title={behind.join(', ')}>{behindText(behind.length)}</span></Notice>}
      <Reachability status={status} />
      <AlertGroup id={name} alerts={openAlerts(name)} />
      <div class="grid grid-cols-2 md:grid-cols-3 xl:grid-cols-6 gap-3">
        <Tile compact size="lg" label="Control plane" tone={!status ? 'muted' : cpDown === 0 ? 'good' : 'bad'} value={status ? `${cps.length - cpDown}/${cps.length}` : '—'} sub={spec.controlPlane.vip ? `VIP ${spec.controlPlane.vip}` : 'no VIP: endpoint is the first control plane'} />
        <Tile compact size="lg" label="etcd quorum" tone={!status ? 'muted' : status.etcd.healthy ? 'good' : 'bad'} value={status ? `${status.etcd.members}/${status.etcd.expected}` : '—'} sub={status?.etcd.leader ? `leader ${status.etcd.leader}` : status?.etcd.alarms?.join(', ') || 'no leader reported'} />
        <Tile compact size="lg" label="Nodes Ready" tone={!t ? 'muted' : t.nodesReady === t.nodes ? 'good' : t.nodesReady === 0 ? 'bad' : 'warn'} value={t ? `${t.nodesReady}/${t.nodes}` : '—'} sub={`${workers} worker${workers === 1 ? '' : 's'}`} />
        <WorkloadsCard cluster={name} pods={t?.pods} service={service} unreachable={down} />
        <Tile compact size="lg" label="Load balancer" tone={down ? 'muted' : status?.platform?.outputs?.ingress_ip ? 'good' : spec.platform.metallb.enabled ? 'warn' : 'muted'} value={status?.platform?.outputs?.ingress_ip ?? (spec.platform.metallb.enabled ? 'pending' : 'off')} sub={spec.platform.metallb.enabled ? `pool ${spec.platform.metallb.range}` : 'MetalLB disabled'} href={`/clusters/${name}/network`} />
        <BackupsCard cluster={name} status={status} interval={spec.backup?.etcd.interval ?? '6h'} />
      </div>
      <div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <CapacityTrend name={name} status={status} />
        <div class="flex flex-col gap-4">
          <Section title="Recent events">
            <div class="panel divide-y divide-border/60 max-h-[260px] overflow-auto">
              {notable.length === 0 && <div class="p-4 text-[13px] text-muted">Nothing worth reporting.</div>}
              {notable.slice(0, 30).map((e) => <EventRow key={e.id} e={e} />)}
            </div>
          </Section>
          <Section title="Recent operations" actions={<a href={`/operations?cluster=${name}`} class="text-[12px] text-accent hover:underline">All activity →</a>}>
            <RecentOperations name={name} />
          </Section>
        </div>
      </div>
    </>
  )
}

function CapacityTrend({ name, status }: { name: string; status: Status | null }) {
  const [range, setRange] = useState('24h')
  const { data: samples, set } = useLive(() => api.samples(name, range), [name, range], [], { onError: 'silent' })
  const t = status?.totals
  useEffect(() => {
    if (!status?.observedAt || !t) return
    const point: Sample = { ts: status.observedAt, cpuMilli: t.cpuMilli, cpuCap: t.cpuCapMilli, memBytes: t.memBytes, memCap: t.memCapBytes, pods: t.pods, ready: t.nodesReady === t.nodes, reachable: status.apiReachable }
    set((prev) => appendWithin(prev, point, spanOf(range)))
  }, [status?.observedAt])
  const series = useMemo(() => {
    const list = samples ?? []
    const pts = (f: (s: Sample) => number, metered = false) => list.map((s) => ({ t: Date.parse(s.ts), v: s.reachable && (!metered || s.memBytes > 0) ? f(s) : null }))
    const lastReachable = list.filter((s) => s.reachable).pop()
    return { cpu: pts((s) => s.cpuMilli, true), mem: pts((s) => s.memBytes, true), pods: pts((s) => s.pods), cpuCap: lastReachable?.cpuCap ?? 0, memCap: lastReachable?.memCap ?? 0 }
  }, [samples])
  const graph = status && !status.apiReachable ? 'bad' : 'accent'
  return (
    <div class="panel p-3 flex flex-col gap-4">
      <div class="flex items-center gap-2">
        <span class="label">Capacity trend</span>
        <RangeButtons value={range} onChange={setRange} />
      </div>
      <Sparkline label="CPU used" points={series.cpu} max={t?.cpuCapMilli || series.cpuCap} format={fmt.cores} height={84} span={spanOf(range)} tone={graph} />
      <Sparkline label="Memory used" points={series.mem} max={t?.memCapBytes || series.memCap} format={fmt.bytes} height={84} span={spanOf(range)} tone={graph} />
      <Sparkline label="Pods" points={series.pods} max={t?.podCap || undefined} format={String} height={84} span={spanOf(range)} tone={graph} />
    </div>
  )
}

function RecentOperations({ name }: { name: string }) {
  const recent = [...opsFor(name)].sort((a, b) => b.id - a.id).slice(0, 5)
  return (
    <div class="panel divide-y divide-border/60">
      {recent.length === 0 && <div class="p-4 text-[13px] text-muted">Nothing yet.</div>}
      {recent.map((o) => (
        <a key={o.id} href={`/operations/${o.id}`} class="flex items-center gap-3 px-4 py-2 text-[13px] hover:bg-panel-2">
          <StatusDot tone={stateTone(o.status)} pulse={o.status === 'running'} />
          <span class="font-medium">{fmt.kind(o.kind)}</span>
          <span class="text-muted">#{o.id}</span>
          <span class="ml-auto text-muted">{fmt.datetime(o.startedAt)}</span>
          <Pill tone={stateTone(o.status)}>{o.status}</Pill>
        </a>
      ))}
    </div>
  )
}

function Reachability({ status }: { status: Status | null }) {
  if (!status) return null
  const blind = status.observer === 'offline'
  const nodes = status.nodes
  const up = nodes.filter((n) => n.talosReachable).length
  const item = (label: string, ok: boolean | null, detail: string) => (
    <span class="flex items-center gap-1.5" title={detail}>
      <StatusDot tone={ok === null ? 'muted' : ok ? 'good' : 'bad'} />
      <span>{label}</span>
      <span class="text-muted">{detail}</span>
    </span>
  )
  return (
    <div class={`panel px-3 py-2 text-[12.5px] flex flex-wrap items-center gap-x-5 gap-y-1 ${blind ? 'border-warn/50' : ''}`}>
      {blind
        ? <span class="text-warn">Kubit cannot reach the network{status.observerError ? ` (${status.observerError})` : ''}; showing the last known state.</span>
        : <>
          {item('Talos API', up === nodes.length, `${up}/${nodes.length} nodes`)}
          {item('Kubernetes API', status.apiReachable, status.apiReachable ? 'reachable' : shortErr(status.apiError))}
          {item('etcd', status.etcd.healthy, `${status.etcd.members}/${status.etcd.expected} members`)}
        </>}
      <span class="ml-auto"><SeenAgo contact={status.lastContactAt} observed={status.observedAt} blind={blind} /></span>
    </div>
  )
}

function shortErr(e?: string) {
  if (!e) return 'unreachable'
  const i = e.lastIndexOf(': ')
  return i >= 0 ? e.slice(i + 2) : e
}

function WorkloadsCard({ cluster, pods, service, unreachable }: { cluster: string; pods?: number; service: ServiceHealth | null; unreachable: boolean }) {
  const namespaces = useNamespaces(cluster)
  if (unreachable) return <Tile compact size="lg" label="Workloads" tone="bad" value="—" sub="API unreachable" href={`/clusters/${cluster}/workloads?view=pods`} />
  const platform = new Set((namespaces ?? []).filter((n) => n.platform).map((n) => n.name))
  const controllers = (service?.workloads ?? []).filter((w) => w.kind !== 'Job' && w.kind !== 'CronJob')
  const down = controllers.filter((w) => !w.available)
  const failing = (service?.pods ?? []).filter((p) => p.phase !== 'Running' && p.phase !== 'Succeeded' && p.phase !== 'Pending')
  const first = down[0]?.namespace ?? failing[0]?.namespace
  const scope = first !== undefined && platform.has(first) ? 'platform' : 'apps'
  const all = service?.pods ?? []
  const apps = all.filter((p) => !platform.has(p.namespace)).length
  const value = service && namespaces ? `${apps} app pod${apps === 1 ? '' : 's'}` : pods === undefined ? '—' : `${pods} pods`
  const sub = !service ? 'no service health yet' : down.length + failing.length === 0 ? `${all.length - apps} platform pods · all ${controllers.length} controllers available` : [down.length ? `${down.length} controller${down.length === 1 ? '' : 's'} unavailable` : '', failing.length ? `${failing.length} pod${failing.length === 1 ? '' : 's'} failing` : ''].filter(Boolean).join(', ')
  const tone: Tone = !service ? 'muted' : down.length > 0 ? 'bad' : failing.length > 0 ? 'warn' : 'good'
  return <Tile compact size="lg" label="Workloads" tone={tone} value={value} sub={sub} href={`/clusters/${cluster}/workloads?view=pods&scope=${scope}`} />
}

function BackupsCard({ cluster, status, interval: spec }: { cluster: string; status?: { lastSnapshotAt?: string; snapshotInterval?: string } | null; interval: string }) {
  useEffect(() => { if (!snapshots.value.has(cluster)) loadSnapshots(cluster) }, [cluster])
  const latest = (snapshots.value.get(cluster) ?? []).find((s) => s.status === 'ok')
  const interval = parseDuration(status?.snapshotInterval ?? spec)
  const at = latest?.ts ?? status?.lastSnapshotAt
  const late = interval > 0 && (!at || (nowEvery(60_000) - Date.parse(at)) / 1000 > 2 * interval)
  const sub = interval === 0 ? 'schedule off' : `every ${status?.snapshotInterval ?? spec}`
  return <Tile compact size="lg" label="Backups" tone={!at || late ? 'warn' : 'good'} value={at ? <Ago iso={at} fresh="just now" /> : 'none'} title={at ? fmt.datetime(at) : undefined} sub={late && at ? `behind schedule · ${sub}` : sub} href={`/clusters/${cluster}/backups`} />
}

function parseDuration(s: string): number {
  const m = /^(\d+(?:\.\d+)?)(h|m|s)$/.exec(s.trim())
  if (!m) return s === '0' ? 0 : 6 * 3600
  return Number(m[1]) * ({ h: 3600, m: 60, s: 1 }[m[2]] ?? 1)
}
