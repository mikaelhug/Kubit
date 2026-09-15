import { useEffect, useState } from 'preact/hooks'
import { useLocation } from 'preact-iso'
import { fmt, type Operation } from '../api'
import { clusters, loadOperationLog, operations, reloadOperations } from '../store'
import { DataTable, type Column } from '../components/DataTable'
import { OperationView } from '../components/ActivityDrawer'
import { Breadcrumbs, Pill, Section, stateTone } from '../components/ui'
import { AuditLog } from '../components/AuditLog'

/** All operations across clusters; /operations/:id shows one with its steps and log. */
export function Operations({ id }: { id?: string }) {
  useEffect(() => { reloadOperations() }, [])
  const { query } = useLocation()
  const [cluster, setCluster] = useState(query.cluster ?? '')
  useEffect(() => { setCluster(query.cluster ?? '') }, [query.cluster])
  const rows = [...operations.value.values()].filter((o) => !cluster || o.cluster === cluster)
  const selected = id ? operations.value.get(Number(id)) : undefined
  useEffect(() => { if (id) loadOperationLog(Number(id)).catch(() => {}) }, [id])

  if (id) return (
    <div class="p-6 flex flex-col gap-4">
      <Breadcrumbs items={[{ label: 'Activity', href: '/operations' }, { label: `#${id}` }]} />
      {selected ? (
        <>
          <div class="flex flex-wrap items-center gap-3">
            <h1 class="text-xl font-semibold">{fmt.kind(selected.kind)}</h1>
            <Pill tone={stateTone(selected.status)}>{selected.status}</Pill>
            {selected.cluster && <a href={`/clusters/${selected.cluster}/overview`} class="text-accent hover:underline">{selected.cluster}</a>}
            <span class="text-[13px] text-muted num">{fmt.datetime(selected.startedAt)} · {fmt.duration(selected.startedAt, selected.finishedAt)}</span>
            {selected.kind === 'platform.plan' && selected.status === 'done' && <a href={`/clusters/${selected.cluster}/addons/${selected.id}`} class="btn !py-1">Review plan</a>}
          </div>
          <div class="panel h-[70vh] flex flex-col overflow-hidden"><OperationView id={selected.id} tall /></div>
        </>
      ) : <div class="text-muted">Operation #{id} not found.</div>}
    </div>
  )

  const columns: Column<Operation>[] = [
    { id: 'id', header: '#', sort: (o) => o.id, align: 'right', cell: (o) => <a href={`/operations/${o.id}`} class="hover:underline">{o.id}</a> },
    { id: 'kind', header: 'Operation', sort: (o) => o.kind, cell: (o) => <a href={`/operations/${o.id}`} class="font-medium hover:underline">{fmt.kind(o.kind)}</a> },
    { id: 'cluster', header: 'Cluster', sort: (o) => o.cluster, cell: (o) => o.cluster ? <a href={`/clusters/${o.cluster}/overview`} class="text-accent hover:underline">{o.cluster}</a> : <span class="text-muted">—</span> },
    { id: 'status', header: 'Status', sort: (o) => o.status, cell: (o) => <Pill tone={stateTone(o.status)}>{o.status}</Pill> },
    { id: 'steps', header: 'Steps', cell: (o) => { const s = o.steps ?? []; const r = s.find((x) => x.status === 'running'); return <span class="text-[12px] text-muted">{r ? r.title : s.length ? `${s.filter((x) => x.status === 'done').length}/${s.length}` : '—'}</span> } },
    { id: 'started', header: 'Started', sort: (o) => o.startedAt, cell: (o) => <span class="num text-muted">{fmt.datetime(o.startedAt)}</span> },
    { id: 'duration', header: 'Duration', align: 'right', cell: (o) => <span class="num">{fmt.duration(o.startedAt, o.finishedAt)}</span> },
  ]
  return (
    <div class="p-6 flex flex-col gap-4">
      <Section title="Activity" help="Every operation Kubit has run, with steps, logs and artefacts. Running ones also appear in the bottom drawer (press a)."
        actions={
          <select class="input !py-1 w-auto" value={cluster} onChange={(e) => { const v = (e.target as HTMLSelectElement).value; setCluster(v); history.replaceState(null, '', v ? `/operations?cluster=${v}` : '/operations') }} aria-label="Filter by cluster">
            <option value="">All clusters</option>
            {clusters.value.map((c) => <option key={c.name} value={c.name}>{c.name}</option>)}
          </select>
        }>
        <DataTable id="ops" columns={columns} rows={rows} rowKey={(o) => String(o.id)} defaultSort={{ id: 'id', dir: 'desc' }} />
      </Section>
      <AuditLog cluster={cluster || undefined} />
    </div>
  )
}
