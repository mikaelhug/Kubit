import { useMemo } from 'preact/hooks'
import { api, type Workload } from '../../api'
import { DataTable, withoutColumn } from '../../components/DataTable'
import { NamespaceScope, useNamespaceScope } from '../../components/NamespaceScope'
import { PodTable } from '../../components/PodTable'
import { Tabs } from '../../components/Tabs'
import { Age } from '../../components/Time'
import { ErrorBox, Notice, Section, StatusDot } from '../../components/ui'
import { useQueryParams } from '../../query'
import { createdSort } from '../../time'
import { useLive } from '../../useLive'
import type { ClusterCtx } from './ClusterPage'

export function Workloads({ ctx }: { ctx: ClusterCtx }) {
  const { name, cluster } = ctx
  const scope = [[name, 'workloads']] as const
  const { data: workloads, error: wError } = useLive(() => api.workloads(name), [name], scope)
  const s = useNamespaceScope(name)
  const [query, setQuery] = useQueryParams()
  const owner = (workloads ?? []).find((w) => w.selector && `${w.kind}/${w.namespace}/${w.name}` === query.owner)
  const { data: pods, error: pError } = useLive(() => api.pods(name, owner?.namespace ?? '', owner?.selector ?? ''), [name, owner?.namespace, owner?.selector], scope)
  const view = query.view === 'pods' ? 'pods' : 'controllers'
  const nodeFilter = query.node ?? ''
  const macOf = useMemo(() => new Map(cluster.spec.spec.nodes.map((n) => [n.hostname, n.mac])), [cluster])
  const nodeHref = useMemo(() => (node: string) => { const mac = macOf.get(node); return mac ? `/machines/${mac}` : `/clusters/${name}/nodes` }, [macOf, name])
  const wl = useMemo(() => (workloads ?? []).filter((w) => s.keep(w.namespace)), [workloads, s.keep])
  const pl = useMemo(() => (pods ?? []).filter((p) => s.keep(p.namespace) && (!nodeFilter || p.node === nodeFilter)), [pods, s.keep, nodeFilter])
  const nodeNames = [...new Set((pods ?? []).map((p) => p.node).filter((n): n is string => !!n))].sort()
  const unhealthy = wl.filter((w) => !w.available && w.kind !== 'Job' && w.kind !== 'CronJob').length
  const empty = (what: string) => s.ns ? `Nothing in ${s.ns}.` : s.scope === 'apps' ? `No app ${what}.` : `No ${what}.`

  const wcols = useMemo(() => withoutColumn<Workload>([
    { id: 'ns', header: 'Namespace', sort: (w) => w.namespace, cell: (w) => w.namespace },
    { id: 'kind', header: 'Kind', sort: (w) => w.kind, cell: (w) => w.kind },
    { id: 'name', header: 'Name', sort: (w) => w.name, cell: (w) => <button class="font-medium hover:underline text-left" onClick={() => setQuery({ ns: w.namespace, view: 'pods', owner: w.selector ? `${w.kind}/${w.namespace}/${w.name}` : undefined })}>{w.name}</button> },
    { id: 'ready', header: 'Ready', sort: (w) => w.ready / Math.max(1, w.desired), cell: (w) => <span class="flex items-center gap-2"><StatusDot tone={w.available ? 'good' : w.ready > 0 ? 'warn' : 'bad'} /><span>{w.ready}/{w.desired}</span></span> },
    { id: 'images', header: 'Images', text: (w) => w.images, cell: (w) => <span class="mono text-[12px] text-muted truncate inline-block max-w-[420px]" title={w.images}>{w.images}</span> },
    { id: 'age', header: 'Age', sort: createdSort, cell: (w) => <span class="text-muted"><Age at={w.createdAt} fallback={w.age} /></span> },
  ], 'ns', !!s.ns), [s.ns, setQuery])
  const loading = workloads === null && !wError

  return (
    <>
      <Section title="Workloads">
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
        {view === 'pods' && owner && (
          <span class="w-fit inline-flex items-center gap-1.5 rounded-[var(--r)] border border-border bg-panel-2 px-2 py-0.5 text-[12px]"><span class="text-muted">{owner.kind}</span><span class="mono">{owner.name}</span><button class="text-muted hover:text-text" aria-label="Show all pods" onClick={() => setQuery({ owner: undefined })}>✕</button></span>
        )}
        <Tabs active={view} onSelect={(v) => setQuery({ view: v === 'pods' ? v : undefined, owner: undefined })} tabs={[{ id: 'controllers', label: 'Controllers', badge: wl.length }, { id: 'pods', label: 'Pods', badge: pl.length }]} />
        {view === 'controllers' && <DataTable loading={loading || s.loading} id="workloads" columns={wcols} rows={wl} rowKey={(w) => `${w.kind}/${w.namespace}/${w.name}`} defaultSort={{ id: 'ns', dir: 'asc' }} empty={empty('workloads')} />}
        {view === 'pods' && <PodTable cluster={name} id="pods" pods={pl} loading={loading || s.loading} empty={empty('pods')} hide={s.ns ? ['ns'] : []} nodeHref={nodeHref} />}
      </Section>
    </>
  )
}
