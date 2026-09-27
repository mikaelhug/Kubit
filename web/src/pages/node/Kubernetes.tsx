import { fmt, type NodeDetail, type PodSummary } from '../../api'
import { DataTable, type Column } from '../../components/DataTable'
import { Age } from '../../components/Time'
import { Notice, Pill, Section, StatusDot } from '../../components/ui'
import { createdSort } from '../../time'
import { phaseTone } from '../../tone'

const columns: Column<PodSummary>[] = [
  { id: 'ns', header: 'Namespace', sort: (p) => p.namespace, cell: (p) => p.namespace },
  { id: 'name', header: 'Pod', sort: (p) => p.name, mono: true, cell: (p) => p.name },
  { id: 'phase', header: 'Phase', sort: (p) => p.phase, cell: (p) => <Pill tone={phaseTone(p.phase)}>{p.phase}</Pill> },
  { id: 'ready', header: 'Ready', cell: (p) => p.ready },
  { id: 'restarts', header: 'Restarts', align: 'right', sort: (p) => p.restarts, cell: (p) => <span class={p.restarts > 3 ? 'text-warn' : ''}>{p.restarts}</span> },
  { id: 'owner', header: 'Owner', sort: (p) => p.owner ?? '', cell: (p) => p.owner || '—' },
  { id: 'cpu', header: 'CPU use / req', align: 'right', sort: (p) => p.usageCpuMilli ?? 0, cell: (p) => <>{fmt.cores(p.usageCpuMilli ?? 0)}<span class="text-muted"> / {p.cpuMilli ? fmt.cores(p.cpuMilli) : '—'}</span></> },
  { id: 'mem', header: 'Mem use / req', align: 'right', sort: (p) => p.usageMemBytes ?? 0, cell: (p) => <>{fmt.bytes(p.usageMemBytes ?? 0)}<span class="text-muted"> / {p.memBytes ? fmt.bytes(p.memBytes) : '—'}</span></> },
  { id: 'age', header: 'Age', sort: createdSort, cell: (p) => <span class="text-muted"><Age at={p.createdAt} fallback={p.age} /></span> },
]

export function KubernetesTab({ k8s, err }: { k8s: NodeDetail | null; err: string | null }) {
  if (err) return <Notice tone={err.includes('not a cluster member') ? 'muted' : 'bad'}>{err}</Notice>
  if (!k8s) return <div class="text-muted">Loading</div>
  const pods = k8s.pods ?? []
  return (
    <div class="flex flex-col gap-5">
      <Section title="Conditions">
        <div class="panel divide-y divide-border/60">
          {(k8s.conditions ?? []).map((c) => {
            const bad = (c.type === 'Ready') !== (c.status === 'True')
            return (
              <div key={c.type} class="flex items-center gap-3 px-4 py-2 text-[13px]">
                <StatusDot tone={bad ? 'bad' : 'good'} />
                <span class="w-40 font-medium">{c.type}</span>
                <span class="mono w-14">{c.status}</span>
                <span class="text-muted truncate" title={c.message}>{c.message}</span>
                <span class="ml-auto text-muted text-[12px]">since {fmt.datetime(c.since ?? '')}</span>
              </div>
            )
          })}
        </div>
      </Section>
      <Section title={`Pods on this node (${pods.length})`} help="Usage from metrics-server; requests from the pod specs.">
        <DataTable id="node-pods" columns={columns} rows={pods} rowKey={(p) => p.namespace + '/' + p.name} defaultSort={{ id: 'ns', dir: 'asc' }} />
      </Section>
      <Section title="Labels">
        <div class="panel p-3 flex flex-wrap gap-1.5">
          {Object.entries(k8s.labels ?? {}).sort().map(([k, v]) => <span key={k} class="mono text-[11.5px] rounded bg-panel-2 px-1.5 py-0.5">{k}{v ? `=${v}` : ''}</span>)}
        </div>
      </Section>
    </div>
  )
}
