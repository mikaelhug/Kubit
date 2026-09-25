import { useEffect, useState } from 'preact/hooks'
import { api, fmt, podLogsUrl, type PodEvent, type PodSummary, type Workload } from '../../api'
import { DataTable, type Column } from '../../components/DataTable'
import { AlertPill, Dialog, ErrorBox, Notice, Pill, Section, StatusDot } from '../../components/ui'
import { clusters, openAlert, refreshKey } from '../../store'
import { NamespaceScope, useNamespaceScope } from '../../components/NamespaceScope'
import { Tabs } from '../../components/Tabs'
import { LogStream } from '../../components/LogStream'
import type { ClusterCtx } from './ClusterPage'

const phaseTone = (p: string) => p === 'Running' || p === 'Succeeded' ? 'good' : p === 'Pending' || p === 'ContainerCreating' ? 'warn' : 'bad'

export function Workloads({ ctx }: { ctx: ClusterCtx }) {
  const { name } = ctx
  const [workloads, setWorkloads] = useState<Workload[]>([])
  const [loaded, setLoaded] = useState(false)
  const [pods, setPods] = useState<PodSummary[]>([])
  const s = useNamespaceScope(name)
  const [error, setError] = useState<string | null>(null)
  const [pod, setPod] = useState<PodSummary | null>(null)
  const query = typeof location !== 'undefined' ? new URLSearchParams(location.search) : new URLSearchParams()
  const [view, setView] = useState<'controllers' | 'pods'>(query.get('view') === 'pods' ? 'pods' : 'controllers')
  const [nodeFilter, setNodeFilter] = useState(query.get('node') ?? '')
  const nodeHref = (hostname: string) => { const mac = clusters.value.find((c) => c.name === name)?.spec.spec.nodes.find((n) => n.hostname === hostname)?.mac; return mac ? `/machines/${mac}` : `/clusters/${name}/nodes` }
  const load = () => {
    api.workloads(name).then(setWorkloads).catch((e) => setError(e.message)).finally(() => setLoaded(true))
    api.pods(name).then(setPods).catch((e) => setError(e.message))
  }
  useEffect(() => { load() }, [name, refreshKey(name, 'workloads')]) // eslint-disable-line
  const wl = workloads.filter((w) => s.keep(w.namespace))
  const nodeNames = [...new Set(pods.map((p) => p.node).filter((n): n is string => !!n))].sort()
  const pl = pods.filter((p) => s.keep(p.namespace) && (!nodeFilter || p.node === nodeFilter))
  const unhealthy = wl.filter((w) => !w.available && w.kind !== 'Job' && w.kind !== 'CronJob').length
  const empty = (what: string) => s.ns ? `Nothing in ${s.ns}.` : s.scope === 'apps' ? `No app ${what} yet; Kubernetes and Kubit's add-ons are under Platform.` : `No ${what}.`

  const wcols: Column<Workload>[] = [
    { id: 'ns', header: 'Namespace', sort: (w) => w.namespace, cell: (w) => w.namespace },
    { id: 'kind', header: 'Kind', sort: (w) => w.kind, cell: (w) => w.kind },
    { id: 'name', header: 'Name', sort: (w) => w.name, cell: (w) => <span class="flex items-center gap-2"><button class="font-medium hover:underline text-left" onClick={() => { s.set({ ns: w.namespace }); setView('pods') }}>{w.name}</button><AlertPill e={openAlert(name, w.kind, w.namespace, w.name)} /></span> },
    { id: 'ready', header: 'Ready', sort: (w) => w.ready / Math.max(1, w.desired), cell: (w) => <span class="flex items-center gap-2"><StatusDot tone={w.available ? 'good' : w.ready > 0 ? 'warn' : 'bad'} /><span class="num">{w.ready}/{w.desired}</span></span> },
    { id: 'images', header: 'Images', text: (w) => w.images, cell: (w) => <span class="mono text-[12px] text-muted truncate inline-block max-w-[420px]" title={w.images}>{w.images}</span> },
    { id: 'age', header: 'Age', cell: (w) => <span class="num text-muted">{w.age}</span> },
  ]
  const pcols: Column<PodSummary>[] = [
    { id: 'ns', header: 'Namespace', sort: (p) => p.namespace, cell: (p) => p.namespace },
    { id: 'name', header: 'Pod', sort: (p) => p.name, mono: true, cell: (p) => <span class="flex items-center gap-2"><button class="hover:underline text-left" onClick={() => setPod(p)}>{p.name}</button><AlertPill e={openAlert(name, 'Pod', p.namespace, p.name)} /></span> },
    { id: 'phase', header: 'Phase', sort: (p) => p.phase, cell: (p) => <Pill tone={phaseTone(p.phase)}>{p.phase}</Pill> },
    { id: 'ready', header: 'Ready', cell: (p) => <span class="num">{p.ready}</span> },
    { id: 'restarts', header: 'Restarts', align: 'right', sort: (p) => p.restarts, cell: (p) => <span class={p.restarts > 3 ? 'text-warn' : ''}>{p.restarts}</span> },
    { id: 'node', header: 'Node', sort: (p) => p.node ?? '', cell: (p) => p.node ? <a href={nodeHref(p.node)} class="hover:underline">{p.node}</a> : '—' },
    { id: 'cpu', header: 'CPU', align: 'right', sort: (p) => p.usageCpuMilli ?? 0, cell: (p) => fmt.cores(p.usageCpuMilli ?? 0) },
    { id: 'mem', header: 'Memory', align: 'right', sort: (p) => p.usageMemBytes ?? 0, cell: (p) => fmt.bytes(p.usageMemBytes ?? 0) },
    { id: 'age', header: 'Age', cell: (p) => <span class="num text-muted">{p.age}</span> },
  ]

  return (
    <>
      <Section title="Workloads" help="Apps are the namespaces outside Kubernetes and Kubit's add-ons. Read-only; change them with kubectl or k9s."
        actions={<a class="btn" href={`/api/v1/clusters/${name}/kubeconfig`} download="kubeconfig">Kubeconfig</a>}>
        <ErrorBox error={error} />
        <div class="flex flex-wrap items-center gap-2">
          <NamespaceScope s={s} rows={view === 'pods' ? pods.map((p) => p.namespace) : workloads.map((w) => w.namespace)} />
          {view === 'pods' && (
            <select class="input !w-48" value={nodeFilter} aria-label="Node" onChange={(e) => setNodeFilter((e.target as HTMLSelectElement).value)}>
              <option value="">All nodes</option>
              {nodeNames.map((n) => <option key={n} value={n}>{n}</option>)}
            </select>
          )}
        </div>
        {unhealthy > 0 && <Notice tone="warn">{unhealthy} controller{unhealthy === 1 ? '' : 's'} below desired replicas.</Notice>}
        <Tabs active={view} onSelect={(v) => setView(v as any)} tabs={[{ id: 'controllers', label: 'Controllers', badge: wl.length }, { id: 'pods', label: 'Pods', badge: pl.length }]} />
        {view === 'controllers' && <DataTable loading={!loaded || s.loading} id="workloads" columns={s.ns ? wcols.filter((c) => c.id !== 'ns') : wcols} rows={wl} rowKey={(w) => `${w.kind}/${w.namespace}/${w.name}`} defaultSort={{ id: 'ns', dir: 'asc' }} empty={empty('workloads')} />}
        {view === 'pods' && <DataTable loading={!loaded || s.loading} id="pods" columns={s.ns ? pcols.filter((c) => c.id !== 'ns') : pcols} rows={pl} rowKey={(p) => `${p.namespace}/${p.name}`} defaultSort={{ id: 'ns', dir: 'asc' }} empty={empty('pods')} />}
      </Section>
      {pod && <PodDialog cluster={name} pod={pod} onClose={() => setPod(null)} />}
    </>
  )
}

function PodDialog({ cluster, pod, onClose }: { cluster: string; pod: PodSummary; onClose: () => void }) {
  const [tab, setTab] = useState<'logs' | 'events'>('logs')
  const [container, setContainer] = useState(pod.containers?.[0] ?? '')
  const [follow, setFollow] = useState(false)
  const [events, setEvents] = useState<PodEvent[]>([])
  useEffect(() => { api.podEvents(cluster, pod.namespace, pod.name).then(setEvents).catch(() => {}) }, [cluster, pod, refreshKey(cluster, 'workloads')])
  return (
    <Dialog title={`${pod.namespace} / ${pod.name}`} onClose={onClose} width="max-w-5xl">
      <div class="flex flex-wrap items-center gap-3 text-[13px]">
        <Pill tone={phaseTone(pod.phase)}>{pod.phase}</Pill>
        <span>ready {pod.ready}</span><span>restarts {pod.restarts}</span>
        {pod.node && <span>on <span class="mono">{pod.node}</span></span>}
        {pod.owner && <span class="text-muted">owned by {pod.owner}</span>}
        <span class="text-muted">age {pod.age}</span>
      </div>
      <Tabs active={tab} onSelect={(t) => setTab(t as any)} tabs={[{ id: 'logs', label: 'Logs' }, { id: 'events', label: 'Events', badge: events.length }]} />
      {tab === 'logs' && (
        <div class="flex flex-col gap-2">
          {(pod.containers?.length ?? 0) > 1 && (
            <select class="input !w-64" value={container} onChange={(e) => setContainer((e.target as HTMLSelectElement).value)}>
              {pod.containers!.map((c) => <option key={c} value={c}>{c}</option>)}
            </select>
          )}
          <LogStream url={podLogsUrl(cluster, pod.namespace, pod.name, container, follow)} follow={follow} onFollow={setFollow} />
        </div>
      )}
      {tab === 'events' && (
        <div class="panel divide-y divide-border/60 max-h-[60vh] overflow-auto">
          {events.length === 0 && <div class="p-4 text-muted text-[13px]">No events recorded (Kubernetes keeps them for about an hour).</div>}
          {events.map((e, i) => (
            <div key={i} class="flex gap-3 px-4 py-2 text-[13px]">
              <StatusDot tone={e.type === 'Warning' ? 'warn' : 'good'} />
              <span class="num text-muted whitespace-nowrap">{fmt.when(e.since ?? '')}</span>
              <span class="font-medium whitespace-nowrap">{e.reason}</span>
              <span class="text-muted">×{e.status}</span>
              <span class="min-w-0 break-words">{e.message}</span>
            </div>
          ))}
        </div>
      )}
    </Dialog>
  )
}
