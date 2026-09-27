import { useEffect, useMemo, useState } from 'preact/hooks'
import { fmt, type Operation } from '../api'
import { AuditLog } from '../components/AuditLog'
import { DataTable, type Column } from '../components/DataTable'
import { OperationView } from '../components/OperationView'
import { Tabs } from '../components/Tabs'
import { Elapsed } from '../components/Time'
import { Breadcrumbs, Pill, Section } from '../components/ui'
import { ensureLog, operations, opList, reloadOperations } from '../ops'
import { useQueryParams } from '../query'
import { clusters, connected } from '../store'
import { stateTone } from '../tone'

const columns: Column<Operation>[] = [
  { id: 'id', header: '#', sort: (o) => o.id, align: 'right', cell: (o) => <a href={`/operations/${o.id}`} class="hover:underline">{o.id}</a> },
  { id: 'kind', header: 'Operation', sort: (o) => o.kind, cell: (o) => <a href={`/operations/${o.id}`} class="font-medium hover:underline">{fmt.kind(o.kind)}</a> },
  { id: 'cluster', header: 'Cluster', sort: (o) => o.cluster, cell: (o) => o.cluster ? <a href={`/clusters/${o.cluster}/overview`} class="text-accent hover:underline">{o.cluster}</a> : <span class="text-muted">—</span> },
  { id: 'status', header: 'Status', sort: (o) => o.status, cell: (o) => <Pill tone={stateTone(o.status)}>{o.status}</Pill> },
  { id: 'steps', header: 'Steps', cell: (o) => { const s = (o.steps ?? []).filter((x) => x.status !== 'skipped'); const r = s.find((x) => x.status === 'running'); return <span class="text-[12px] text-muted">{r ? r.title : s.length ? `${s.filter((x) => x.status === 'done').length}/${s.length}` : '—'}</span> } },
  { id: 'started', header: 'Started', sort: (o) => o.startedAt, cell: (o) => <span class="text-muted">{fmt.datetime(o.startedAt)}</span> },
  { id: 'duration', header: 'Duration', align: 'right', cell: (o) => <Elapsed from={o.startedAt} to={o.finishedAt} /> },
]

export function Operations({ id }: { id?: string }) {
  useEffect(() => { reloadOperations() }, [])
  const [query, setQuery] = useQueryParams()
  const cluster = query.cluster ?? ''
  const view = query.view === 'audit' ? 'audit' : 'operations'
  const [failed, setFailed] = useState<string | null>(null)
  useEffect(() => { setFailed(null); if (id) ensureLog(Number(id)).catch((e) => setFailed(e.status === 404 ? `Operation #${id} not found.` : e.message)) }, [id, connected.value])
  const all = opList.value
  const rows = useMemo(() => all.filter((o) => !cluster || o.cluster === cluster), [all, cluster])

  if (id) {
    const selected = operations.value.get(Number(id))
    return (
      <div class="p-6 flex flex-col gap-4">
        <Breadcrumbs items={[{ label: 'Activity', href: '/operations' }, { label: `#${id}` }]} />
        {selected ? (
          <>
            <div class="flex flex-wrap items-center gap-3">
              <h1 class="text-xl font-semibold">{fmt.kind(selected.kind)}</h1>
              <Pill tone={stateTone(selected.status)}>{selected.status}</Pill>
              {selected.cluster && <a href={`/clusters/${selected.cluster}/overview`} class="text-accent hover:underline">{selected.cluster}</a>}
              <span class="text-[13px] text-muted">{fmt.datetime(selected.startedAt)} · <Elapsed from={selected.startedAt} to={selected.finishedAt} /></span>
              {selected.kind === 'platform.plan' && selected.status === 'done' && <a href={`/clusters/${selected.cluster}/addons/${selected.id}`} class="btn btn-sm">Review plan</a>}
            </div>
            <div class="panel h-[70vh] flex flex-col overflow-hidden"><OperationView key={selected.id} id={selected.id} /></div>
          </>
        ) : <div class="text-muted">{failed ?? 'Loading'}</div>}
      </div>
    )
  }

  return (
    <div class="p-6 flex flex-col gap-4">
      <Section title="Activity" help="What Kubit ran, and who did what."
        actions={
          <select class="input !py-1 w-auto" value={cluster} onChange={(e) => setQuery({ cluster: (e.target as HTMLSelectElement).value })} aria-label="Filter by cluster">
            <option value="">All clusters</option>
            {clusters.value.map((c) => <option key={c.name} value={c.name}>{c.name}</option>)}
          </select>
        }>
        <Tabs active={view} onSelect={(v) => setQuery({ view: v === 'audit' ? v : undefined })} tabs={[{ id: 'operations', label: 'Operations', badge: rows.length }, { id: 'audit', label: 'Audit' }]} />
        {view === 'operations' && <DataTable id="ops" columns={columns} rows={rows} rowKey={(o) => String(o.id)} defaultSort={{ id: 'id', dir: 'desc' }} />}
        {view === 'audit' && <AuditLog cluster={cluster || undefined} />}
      </Section>
    </div>
  )
}
