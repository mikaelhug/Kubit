import { useMemo, useRef, useState } from 'preact/hooks'
import { api, authedUrl, fmt, podLogsUrl, type PodSummary, type Workload } from '../../api'
import { DataTable, withoutColumn } from '../../components/DataTable'
import { LogStream } from '../../components/LogStream'
import { NamespaceScope, useNamespaceScope } from '../../components/NamespaceScope'
import { Tabs } from '../../components/Tabs'
import { Age } from '../../components/Time'
import { AlertPill, Dialog, ErrorBox, Notice, Pill, Section, StatusDot } from '../../components/ui'
import { useQueryParams } from '../../query'
import { alertIndex, objectKey } from '../../store'
import { createdSort } from '../../time'
import { phaseTone } from '../../tone'
import { useLive } from '../../useLive'
import type { ClusterCtx } from './ClusterPage'

export function Workloads({ ctx }: { ctx: ClusterCtx }) {
  const { name, cluster } = ctx
  const scope = [[name, 'workloads']] as const
  const { data: workloads, error: wError } = useLive(() => api.workloads(name), [name], scope)
  const { data: pods, error: pError } = useLive(() => api.pods(name), [name], scope)
  const s = useNamespaceScope(name)
  const [podKey, setPodKey] = useState<string | null>(null)
  const [query, setQuery] = useQueryParams()
  const view = query.view === 'pods' ? 'pods' : 'controllers'
  const nodeFilter = query.node ?? ''
  const podOf = (p: PodSummary) => `${p.namespace}/${p.name}`
  const livePod = podKey ? pods?.find((p) => podOf(p) === podKey) : undefined
  const lastPod = useRef<PodSummary | undefined>(undefined)
  if (livePod) lastPod.current = livePod
  const shownPod = podKey ? livePod ?? (lastPod.current && podOf(lastPod.current) === podKey ? lastPod.current : undefined) : undefined
  const alerts = alertIndex(name)
  const macOf = useMemo(() => new Map(cluster.spec.spec.nodes.map((n) => [n.hostname, n.mac])), [cluster])
  const wl = useMemo(() => (workloads ?? []).filter((w) => s.keep(w.namespace)), [workloads, s.keep])
  const pl = useMemo(() => (pods ?? []).filter((p) => s.keep(p.namespace) && (!nodeFilter || p.node === nodeFilter)), [pods, s.keep, nodeFilter])
  const nodeNames = [...new Set((pods ?? []).map((p) => p.node).filter((n): n is string => !!n))].sort()
  const unhealthy = wl.filter((w) => !w.available && w.kind !== 'Job' && w.kind !== 'CronJob').length
  const empty = (what: string) => s.ns ? `Nothing in ${s.ns}.` : s.scope === 'apps' ? `No app ${what}.` : `No ${what}.`

  const wcols = useMemo(() => withoutColumn<Workload>([
    { id: 'ns', header: 'Namespace', sort: (w) => w.namespace, cell: (w) => w.namespace },
    { id: 'kind', header: 'Kind', sort: (w) => w.kind, cell: (w) => w.kind },
    { id: 'name', header: 'Name', sort: (w) => w.name, cell: (w) => <span class="flex items-center gap-2"><button class="font-medium hover:underline text-left" onClick={() => setQuery({ ns: w.namespace, view: 'pods' })}>{w.name}</button><AlertPill e={alerts.get(objectKey(w.kind, w.namespace, w.name))} /></span> },
    { id: 'ready', header: 'Ready', sort: (w) => w.ready / Math.max(1, w.desired), cell: (w) => <span class="flex items-center gap-2"><StatusDot tone={w.available ? 'good' : w.ready > 0 ? 'warn' : 'bad'} /><span>{w.ready}/{w.desired}</span></span> },
    { id: 'images', header: 'Images', text: (w) => w.images, cell: (w) => <span class="mono text-[12px] text-muted truncate inline-block max-w-[420px]" title={w.images}>{w.images}</span> },
    { id: 'age', header: 'Age', sort: createdSort, cell: (w) => <span class="text-muted"><Age at={w.createdAt} fallback={w.age} /></span> },
  ], 'ns', !!s.ns), [alerts, s.ns, setQuery])
  const pcols = useMemo(() => withoutColumn<PodSummary>([
    { id: 'ns', header: 'Namespace', sort: (p) => p.namespace, cell: (p) => p.namespace },
    { id: 'name', header: 'Pod', sort: (p) => p.name, mono: true, cell: (p) => <span class="flex items-center gap-2"><button class="hover:underline text-left" onClick={() => setPodKey(podOf(p))}>{p.name}</button><AlertPill e={alerts.get(objectKey('Pod', p.namespace, p.name))} /></span> },
    { id: 'phase', header: 'Phase', sort: (p) => p.phase, cell: (p) => <Pill tone={phaseTone(p.phase)}>{p.phase}</Pill> },
    { id: 'ready', header: 'Ready', cell: (p) => p.ready },
    { id: 'restarts', header: 'Restarts', align: 'right', sort: (p) => p.restarts, cell: (p) => <span class={p.restarts > 3 ? 'text-warn' : ''}>{p.restarts}</span> },
    { id: 'node', header: 'Node', sort: (p) => p.node ?? '', cell: (p) => { if (!p.node) return '—'; const mac = macOf.get(p.node); return <a href={mac ? `/machines/${mac}` : `/clusters/${name}/nodes`} class="hover:underline">{p.node}</a> } },
    { id: 'cpu', header: 'CPU', align: 'right', sort: (p) => p.usageCpuMilli ?? 0, cell: (p) => fmt.cores(p.usageCpuMilli ?? 0) },
    { id: 'mem', header: 'Memory', align: 'right', sort: (p) => p.usageMemBytes ?? 0, cell: (p) => fmt.bytes(p.usageMemBytes ?? 0) },
    { id: 'age', header: 'Age', sort: createdSort, cell: (p) => <span class="text-muted"><Age at={p.createdAt} fallback={p.age} /></span> },
  ], 'ns', !!s.ns), [alerts, s.ns, macOf, name])
  const loading = workloads === null && !wError

  return (
    <>
      <Section title="Workloads" help="Read-only; apps are the namespaces outside Kubernetes and the add-ons."
        actions={<a class="btn" href={authedUrl(`/clusters/${name}/kubeconfig`)} download="kubeconfig">Kubeconfig</a>}>
        <ErrorBox error={wError ?? pError} />
        <div class="flex flex-wrap items-center gap-2">
          <NamespaceScope s={s} rows={view === 'pods' ? (pods ?? []).map((p) => p.namespace) : (workloads ?? []).map((w) => w.namespace)} />
          {view === 'pods' && (
            <select class="input !w-48" value={nodeFilter} aria-label="Node" onChange={(e) => setQuery({ node: (e.target as HTMLSelectElement).value })}>
              <option value="">All nodes</option>
              {nodeNames.map((n) => <option key={n} value={n}>{n}</option>)}
            </select>
          )}
        </div>
        {unhealthy > 0 && <Notice tone="warn">{unhealthy} controller{unhealthy === 1 ? '' : 's'} below desired replicas.</Notice>}
        <Tabs active={view} onSelect={(v) => setQuery({ view: v === 'pods' ? v : undefined })} tabs={[{ id: 'controllers', label: 'Controllers', badge: wl.length }, { id: 'pods', label: 'Pods', badge: pl.length }]} />
        {view === 'controllers' && <DataTable loading={loading || s.loading} id="workloads" columns={wcols} rows={wl} rowKey={(w) => `${w.kind}/${w.namespace}/${w.name}`} defaultSort={{ id: 'ns', dir: 'asc' }} empty={empty('workloads')} />}
        {view === 'pods' && <DataTable loading={loading || s.loading} id="pods" columns={pcols} rows={pl} rowKey={(p) => `${p.namespace}/${p.name}`} defaultSort={{ id: 'ns', dir: 'asc' }} empty={empty('pods')} />}
      </Section>
      {shownPod && <PodDialog cluster={name} pod={shownPod} gone={!livePod} onClose={() => setPodKey(null)} />}
    </>
  )
}

function PodDialog({ cluster, pod, gone, onClose }: { cluster: string; pod: PodSummary; gone: boolean; onClose: () => void }) {
  const [tab, setTab] = useState<'logs' | 'events'>('logs')
  const [container, setContainer] = useState(pod.containers?.[0] ?? '')
  const [follow, setFollow] = useState(false)
  const { data: events } = useLive(() => api.podEvents(cluster, pod.namespace, pod.name), [cluster, pod.namespace, pod.name], [[cluster, 'workloads']], { onError: 'silent' })
  const list = events ?? []
  return (
    <Dialog title={`${pod.namespace} / ${pod.name}`} onClose={onClose} width="max-w-5xl">
      <div class="flex flex-wrap items-center gap-3 text-[13px]">
        {gone ? <Pill tone="muted">deleted</Pill> : <Pill tone={phaseTone(pod.phase)}>{pod.phase}</Pill>}
        <span>ready {pod.ready}</span><span>restarts {pod.restarts}</span>
        {pod.node && <span>on <span class="mono">{pod.node}</span></span>}
        {pod.owner && <span class="text-muted">owned by {pod.owner}</span>}
        <span class="text-muted">age <Age at={pod.createdAt} fallback={pod.age} /></span>
      </div>
      <Tabs active={tab} onSelect={(t) => setTab(t as 'logs' | 'events')} tabs={[{ id: 'logs', label: 'Logs' }, { id: 'events', label: 'Events', badge: list.length }]} />
      {tab === 'logs' && (
        <LogStream url={podLogsUrl(cluster, pod.namespace, pod.name, container, follow)} follow={follow} onFollow={setFollow} toolbar={(pod.containers?.length ?? 0) > 1 && (
          <select class="input !w-64" value={container} onChange={(e) => setContainer((e.target as HTMLSelectElement).value)}>
            {pod.containers!.map((c) => <option key={c} value={c}>{c}</option>)}
          </select>
        )} />
      )}
      {tab === 'events' && (
        <div class="panel divide-y divide-border/60 max-h-[60vh] overflow-auto">
          {list.length === 0 && <div class="p-4 text-muted text-[13px]">No events.</div>}
          {list.map((e, i) => (
            <div key={`${e.since ?? ''}:${e.reason ?? ''}:${i}`} class="flex gap-3 px-4 py-2 text-[13px]">
              <StatusDot tone={e.type === 'Warning' ? 'warn' : 'good'} />
              <span class="text-muted whitespace-nowrap">{fmt.when(e.since ?? '')}</span>
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
