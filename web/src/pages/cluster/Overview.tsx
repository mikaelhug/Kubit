import { fmt } from '../../api'
import { Meter, Notice, Pill, Section, StatusDot } from '../../components/ui'
import { operations } from '../../store'
import type { ClusterCtx } from './ClusterPage'

export function Overview({ ctx }: { ctx: ClusterCtx }) {
  const { status, cluster, name } = ctx
  const t = status?.totals
  const spec = cluster.spec.spec
  const recent = [...operations.value.values()].filter((o) => o.cluster === name).sort((a, b) => b.id - a.id).slice(0, 6)
  const cpDown = status ? status.nodes.filter((n) => n.role === 'controlplane' && (!n.talosReachable || !n.ready)).length : 0

  return (
    <>
      {status?.apiError && <Notice tone="warn">Kubernetes API unreachable at {status.endpoint}: {status.apiError}. Readiness and usage come from the last known state.</Notice>}
      <div class="grid grid-cols-2 xl:grid-cols-4 gap-4">
        <Card label="Control plane" tone={!status ? 'muted' : cpDown === 0 ? 'good' : 'bad'} value={status ? `${status.nodes.filter((n) => n.role === 'controlplane').length - cpDown}/${status.nodes.filter((n) => n.role === 'controlplane').length}` : '—'} sub={spec.controlPlane.vip ? `VIP ${spec.controlPlane.vip}` : 'no VIP: endpoint is the first control plane'} />
        <Card label="etcd quorum" tone={!status ? 'muted' : status.etcd.healthy ? 'good' : 'bad'} value={status ? `${status.etcd.members}/${status.etcd.expected}` : '—'} sub={status?.etcd.leader ? `leader ${status.etcd.leader}` : status?.etcd.alarms?.join(', ') || 'no leader reported'} />
        <Card label="Nodes Ready" tone={!t ? 'muted' : t.nodesReady === t.nodes ? 'good' : 'warn'} value={t ? `${t.nodesReady}/${t.nodes}` : '—'} sub={`${spec.nodes.filter((n) => n.role === 'worker').length} worker${spec.nodes.filter((n) => n.role === 'worker').length === 1 ? '' : 's'}`} />
        <Card label="Load balancer" tone={status?.platform?.outputs?.ingress_ip ? 'good' : spec.platform.metallb.enabled ? 'warn' : 'muted'} value={status?.platform?.outputs?.ingress_ip ?? (spec.platform.metallb.enabled ? 'pending' : 'off')} sub={spec.platform.metallb.enabled ? `pool ${spec.platform.metallb.range}` : 'MetalLB disabled'} />
      </div>
      <div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <div class="panel p-4 flex flex-col gap-3">
          <span class="label">Capacity (allocatable)</span>
          <Meter label="CPU" used={t?.cpuMilli ?? 0} cap={t?.cpuCapMilli ?? 0} format={fmt.cores} />
          <Meter label="Memory" used={t?.memBytes ?? 0} cap={t?.memCapBytes ?? 0} format={fmt.bytes} />
          <Meter label="Pods" used={t?.pods ?? 0} cap={t?.podCap ?? 0} format={String} />
          <span class="text-[11px] text-muted">Usage from metrics-server; capacity from node allocatable. Trends arrive with the health watcher (M3).</span>
        </div>
        <Section title="Recent operations" help="What Kubit has done to this cluster; open one for its steps and log.">
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
    </>
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
