import { useMemo, useRef, useState } from 'preact/hooks'
import { api, fmt, podLogsUrl, type PodSummary } from '../api'
import { createdSort } from '../time'
import { phaseTone } from '../tone'
import { useLive } from '../useLive'
import { DataTable, type Column } from './DataTable'
import { LogStream } from './LogStream'
import { Tabs } from './Tabs'
import { Age } from './Time'
import { Dialog, Pill, StatusDot } from './ui'

const podOf = (p: PodSummary) => `${p.namespace}/${p.name}`

export function PodTable({ cluster, pods, id, loading, empty, hide = [], nodeHref }: { cluster: string; pods: PodSummary[]; id: string; loading?: boolean; empty?: string; hide?: string[]; nodeHref?: (node: string) => string }) {
  const [podKey, setPodKey] = useState<string | null>(null)
  const livePod = podKey ? pods.find((p) => podOf(p) === podKey) : undefined
  const lastPod = useRef<PodSummary | undefined>(undefined)
  if (livePod) lastPod.current = livePod
  const shownPod = podKey ? livePod ?? (lastPod.current && podOf(lastPod.current) === podKey ? lastPod.current : undefined) : undefined
  const columns = useMemo<Column<PodSummary>[]>(() => ([
    { id: 'ns', header: 'Namespace', sort: (p) => p.namespace, cell: (p) => p.namespace },
    { id: 'name', header: 'Pod', sort: (p) => p.name, mono: true, cell: (p) => <button class="hover:underline text-left" onClick={() => setPodKey(podOf(p))}>{p.name}</button> },
    { id: 'phase', header: 'Phase', sort: (p) => p.phase, cell: (p) => <Pill tone={phaseTone(p.phase)}>{p.phase}</Pill> },
    { id: 'ready', header: 'Ready', cell: (p) => p.ready },
    { id: 'restarts', header: 'Restarts', align: 'right', sort: (p) => p.restarts, cell: (p) => <span class={p.restarts > 3 ? 'text-warn' : ''}>{p.restarts}</span> },
    { id: 'node', header: 'Node', sort: (p) => p.node ?? '', cell: (p) => p.node ? (nodeHref ? <a href={nodeHref(p.node)} class="hover:underline">{p.node}</a> : p.node) : '—' },
    { id: 'owner', header: 'Owner', sort: (p) => p.owner ?? '', cell: (p) => <span class="text-muted">{p.owner || '—'}</span> },
    { id: 'cpu', header: 'CPU use / req', align: 'right', sort: (p) => p.usageCpuMilli ?? 0, cell: (p) => <>{fmt.cores(p.usageCpuMilli ?? 0)}<span class="text-muted"> / {p.cpuMilli ? fmt.cores(p.cpuMilli) : '—'}</span></> },
    { id: 'mem', header: 'Mem use / req', align: 'right', sort: (p) => p.usageMemBytes ?? 0, cell: (p) => <>{fmt.bytes(p.usageMemBytes ?? 0)}<span class="text-muted"> / {p.memBytes ? fmt.bytes(p.memBytes) : '—'}</span></> },
    { id: 'age', header: 'Age', sort: createdSort, cell: (p) => <span class="text-muted"><Age at={p.createdAt} fallback={p.age} /></span> },
  ] as Column<PodSummary>[]).filter((c) => !hide.includes(c.id)), [hide.join(','), nodeHref])
  return (
    <>
      <DataTable loading={loading} id={id} columns={columns} rows={pods} rowKey={podOf} defaultSort={{ id: hide.includes('ns') ? 'name' : 'ns', dir: 'asc' }} empty={empty} />
      {shownPod && <PodDialog cluster={cluster} pod={shownPod} gone={!livePod} onClose={() => setPodKey(null)} />}
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
