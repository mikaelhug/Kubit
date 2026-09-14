import { fmt, type Operation } from '../../api'
import { operations } from '../../store'
import { DataTable, type Column } from '../../components/DataTable'
import { Pill, Section, stateTone } from '../../components/ui'
import { Stepper } from '../../components/Stepper'
import type { ClusterCtx } from './ClusterPage'

export function ClusterOperations({ ctx }: { ctx: ClusterCtx }) {
  const rows = [...operations.value.values()].filter((o) => o.cluster === ctx.name)
  const columns: Column<Operation>[] = [
    { id: 'id', header: '#', sort: (o) => o.id, align: 'right', cell: (o) => <a href={`/operations/${o.id}`} class="hover:underline">{o.id}</a> },
    { id: 'kind', header: 'Operation', sort: (o) => o.kind, cell: (o) => <a href={`/operations/${o.id}`} class="font-medium hover:underline">{fmt.kind(o.kind)}</a> },
    { id: 'status', header: 'Status', sort: (o) => o.status, cell: (o) => <Pill tone={stateTone(o.status)}>{o.status}</Pill> },
    { id: 'steps', header: 'Progress', cell: (o) => <Progress o={o} /> },
    { id: 'started', header: 'Started', sort: (o) => o.startedAt, cell: (o) => <span class="num text-muted">{fmt.datetime(o.startedAt)}</span> },
    { id: 'duration', header: 'Duration', align: 'right', cell: (o) => <span class="num">{fmt.duration(o.startedAt, o.finishedAt)}</span> },
  ]
  return (
    <Section title="Operations" help="Everything Kubit has done to this cluster. Steps, logs and artefacts (such as plans) are kept.">
      <DataTable id="cluster-ops" columns={columns} rows={rows} rowKey={(o) => String(o.id)} defaultSort={{ id: 'id', dir: 'desc' }} />
    </Section>
  )
}

function Progress({ o }: { o: Operation }) {
  const steps = o.steps ?? []
  if (steps.length === 0) return <span class="text-muted">—</span>
  const done = steps.filter((s) => s.status === 'done').length
  const running = steps.find((s) => s.status === 'running')
  return (
    <span class="flex items-center gap-2" title={steps.map((s) => `${s.status}: ${s.title}`).join('\n')}>
      <span class="inline-flex gap-0.5">{steps.map((s) => <span key={s.id} class={`inline-block h-1.5 w-3 rounded-sm ${s.status === 'done' ? 'bg-good' : s.status === 'running' ? 'bg-accent animate-pulse' : s.status === 'failed' ? 'bg-bad' : 'bg-border'}`} />)}</span>
      <span class="text-[12px] text-muted">{running ? running.title : `${done}/${steps.length}`}</span>
    </span>
  )
}

export { Stepper }
