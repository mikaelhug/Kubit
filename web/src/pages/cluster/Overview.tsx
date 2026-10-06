import { useEffect, useMemo, useState } from 'preact/hooks'
import { api, fmt, type Sample, type Status } from '../../api'
import { AlertGroup, EventRow } from '../../components/Alerts'
import { behindText, useConfigStatus } from '../../configStatus'
import { useNamespaces } from '../../components/NamespaceScope'
import { RangeButtons, Sparkline, spanOf } from '../../components/Sparkline'
import { Ago } from '../../components/Time'
import { Upgrade } from '../../components/Upgrade'
import { Notice, Section, StatusDot, Tile } from '../../components/ui'
import { now } from '../../clock'
import { health, openAlerts, versions } from '../../store'
import { appendWithin } from '../../time'
import { type Tone } from '../../tone'
import { useLive } from '../../useLive'
import { updateText, updatesFor } from '../../versions'
import type { ClusterCtx } from './ClusterPage'

const recoveryKinds = new Set(['talos.back', 'node.ready', 'api.back', 'etcd.healthy', 'cert.renewed'])

export function Overview({ ctx }: { ctx: ClusterCtx }) {
  const { status, cluster, name } = ctx
  const t = status?.totals
  const spec = cluster.spec.spec
  const cps = status ? status.nodes.filter((n) => n.role === 'controlplane') : []
  const cpDown = cps.filter((n) => !n.talosReachable || !n.ready).length
  const events = health.value.get(name) ?? []
  const notable = events.filter((e) => !e.open && (e.severity !== 'info' || recoveryKinds.has(e.kind)))
  const u = updatesFor(spec.talosVersion, spec.kubernetesVersion, versions.value)

  const down = !!status && !status.apiReachable
  const workers = spec.nodes.filter((n) => n.role === 'worker').length
  const behind = useConfigStatus(name)?.behind ?? []

  return (
    <>
      {(u.talos || u.kubernetes) && <Notice tone="info"><span class="flex flex-wrap items-center gap-2"><span>{updateText(u.talos, u.kubernetes)} available.</span><Upgrade cluster={name} talos={u.talos} kubernetes={u.kubernetes} /></span></Notice>}
      {behind.length > 0 && <Notice tone="warn"><span title={behind.join(', ')}>{behindText(behind.length)}</span> <a class="underline" href={`/clusters/${name}/changes`}>Review changes</a></Notice>}
      <Reachability status={status} />
      <AlertGroup alerts={openAlerts(name)} />
      <div class="grid grid-cols-2 md:grid-cols-3 xl:grid-cols-6 gap-3">
        <Tile compact size="lg" label="Control plane" tone={!status ? 'muted' : cpDown === 0 ? 'good' : 'bad'} value={status ? `${cps.length - cpDown}/${cps.length}` : '—'} sub={spec.controlPlane.vip ? `VIP ${spec.controlPlane.vip}` : 'no VIP'} />
        <Tile compact size="lg" label="etcd quorum" tone={!status ? 'muted' : status.etcd.healthy ? 'good' : 'bad'} value={status ? `${status.etcd.members}/${status.etcd.expected}` : '—'} sub={status?.etcd.leader ? `leader ${status.etcd.leader}` : status?.etcd.alarms?.join(', ') || 'no leader reported'} />
        <Tile compact size="lg" label="Nodes Ready" tone={!t ? 'muted' : t.nodesReady === t.nodes ? 'good' : t.nodesReady === 0 ? 'bad' : 'warn'} value={t ? `${t.nodesReady}/${t.nodes}` : '—'} sub={`${workers} worker${workers === 1 ? '' : 's'}`} href={`/clusters/${name}/nodes`} />
        <WorkloadsCard cluster={name} pods={t?.pods} unreachable={down} />
        <Tile compact size="lg" label="Load balancer" tone={down ? 'muted' : status?.ingressIP ? 'good' : spec.platform.metallb.enabled ? 'warn' : 'muted'} value={status?.ingressIP ?? (spec.platform.metallb.enabled ? 'pending' : 'off')} sub={spec.platform.metallb.enabled ? `pool ${spec.platform.metallb.range}` : undefined} href={`/clusters/${name}/network`} />
        <BackupsCard cluster={name} status={status} schedule={spec.backup?.schedule} />
      </div>
      <div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <CapacityTrend name={name} status={status} />
        <div class="flex flex-col gap-4">
          <Section title="Recent events">
            <div class="panel divide-y divide-border/60 max-h-[260px] overflow-auto">
              {notable.length === 0 && <div class="p-4 text-[13px] text-muted">No events.</div>}
              {notable.slice(0, 30).map((e) => <EventRow key={e.id} e={e} />)}
            </div>
          </Section>
        </div>
      </div>
    </>
  )
}

function CapacityTrend({ name, status }: { name: string; status: Status | null }) {
  const [range, setRange] = useState('5m')
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
        </>}
    </div>
  )
}

function shortErr(e?: string) {
  if (!e) return 'unreachable'
  const i = e.lastIndexOf(': ')
  return i >= 0 ? e.slice(i + 2) : e
}

function WorkloadsCard({ cluster, pods, unreachable }: { cluster: string; pods?: number; unreachable: boolean }) {
  const namespaces = useNamespaces(cluster)
  const { data: workloads } = useLive(() => api.workloads(cluster), [cluster], [[cluster, 'workloads']], { onError: 'silent', enabled: !unreachable })
  if (unreachable) return <Tile compact size="lg" label="Workloads" tone="bad" value="—" sub="API unreachable" href={`/clusters/${cluster}/workloads`} />
  const platform = new Set((namespaces ?? []).filter((n) => n.platform).map((n) => n.name))
  const controllers = (workloads ?? []).filter((w) => w.kind !== 'Job' && w.kind !== 'CronJob')
  const down = controllers.filter((w) => !w.available)
  const scope = down[0] && platform.has(down[0].namespace) ? 'platform' : 'apps'
  const value = pods === undefined ? '—' : `${pods} pods`
  const sub = !workloads ? 'loading' : down.length === 0 ? `all ${controllers.length} controllers available` : `${down.length} controller${down.length === 1 ? '' : 's'} unavailable`
  const tone: Tone = !workloads ? 'muted' : down.length > 0 ? 'bad' : 'good'
  return <Tile compact size="lg" label="Workloads" tone={tone} value={value} sub={sub} href={`/clusters/${cluster}/workloads?scope=${scope}`} />
}

const day = 86_400_000

function BackupsCard({ cluster, status, schedule }: { cluster: string; status?: { lastSnapshotAt?: string } | null; schedule?: string }) {
  const at = status?.lastSnapshotAt
  const age = at ? now.value - Date.parse(at) : Infinity
  const tone: Tone = age < day ? 'good' : age < 3 * day ? 'warn' : schedule ? 'bad' : 'muted'
  const value = at ? <Ago iso={at} /> : 'none'
  return <Tile compact size="lg" label="Last etcd snapshot" tone={tone} value={value} title={at ? fmt.datetime(at) : undefined} sub={schedule ? `talos-backup, cron ${schedule}` : 'no schedule in spec.backup'} href={`/clusters/${cluster}/backups`} />
}
