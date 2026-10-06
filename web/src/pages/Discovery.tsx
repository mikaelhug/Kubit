import { useMemo, useState } from 'preact/hooks'
import { api, fmt, type NodeRow } from '../api'
import { DataTable, type Column } from '../components/DataTable'
import { KindPill, TypePill } from '../components/Machine'
import { AddMachines } from '../components/AddMachine'
import { ScanBox } from '../components/ScanBox'
import { Code, KeyValue, Notice, Pill, SeenAgo, Section } from '../components/ui'
import { addable, installCandidates, lastSeenOf, modelOf, specsOf } from '../machine'
import { live, machineList, toast } from '../store'
import { useLive } from '../useLive'

const hardware = (n: NodeRow) => {
  const inv = n.inventory
  if (!inv) return <span class="text-muted">—</span>
  const disks = installCandidates(n)
  return (
    <span class="whitespace-nowrap" title={disks.map((d) => `${d.devPath} ${fmt.bytes(d.sizeBytes)}${d.rotational ? ' HDD' : ''}`).join(', ')}>
      {specsOf(inv)}{disks.length > 1 ? ` +${disks.length - 1}` : ''}
      <span class="block text-[10px] text-muted">{inv.arch || n.arch}{inv.kvm ? ' · kvm' : ''}</span>
    </span>
  )
}

const machineCell = (n: NodeRow) => (
  <a href={`/machines/${n.mac}`} class="flex flex-col min-w-0 hover:underline">
    <span class="font-medium truncate">{(n.kind === 'member' && n.hostname) || n.inventory?.hostname || modelOf(n)}</span>
    <span class="text-[10px] text-muted mono truncate">{n.mac}</span>
  </a>
)

const readyColumns = (selected: Set<string>, toggle: (mac: string) => void, all: NodeRow[], setAll: (on: boolean) => void): Column<NodeRow>[] => [
  { id: 'pick', header: <input type="checkbox" aria-label="Select all" checked={all.length > 0 && all.every((n) => selected.has(n.mac))} onChange={(e) => setAll((e.target as HTMLInputElement).checked)} />, width: '28px', cell: (n) => <input type="checkbox" aria-label={`Select ${n.ip}`} checked={selected.has(n.mac)} onChange={() => toggle(n.mac)} /> },
  { id: 'machine', header: 'Machine', sort: (n) => n.ip, text: (n) => `${modelOf(n)} ${n.mac} ${n.serial ?? ''}`, cell: machineCell },
  { id: 'ip', header: 'Address', mono: true, sort: (n) => n.ip, cell: (n) => n.ip },
  { id: 'type', header: 'Type', cell: (n) => <TypePill m={n} /> },
  { id: 'hardware', header: 'Hardware', sort: (n) => n.inventory?.memoryBytes ?? 0, cell: hardware },
  { id: 'talos', header: 'Talos', mono: true, cell: (n) => n.inventory?.talosVersion || n.talosVersion || '—' },
  { id: 'seen', header: 'Seen', sort: (n) => lastSeenOf(n), cell: (n) => <SeenAgo observed={lastSeenOf(n)} /> },
  { id: 'add', header: '', align: 'right', cell: (n) => <AddMachines machines={[n]} primary={false} /> },
]

const kindHint: Partial<Record<NodeRow['kind'], string>> = {
  configured: 'Reset it or wipe its disk to add it.',
}

const other: Column<NodeRow>[] = [
  { id: 'machine', header: 'Machine', sort: (n) => n.ip, text: (n) => `${modelOf(n)} ${n.mac}`, cell: machineCell },
  { id: 'ip', header: 'Address', mono: true, sort: (n) => n.ip, cell: (n) => n.ip || '—' },
  { id: 'state', header: 'State', sort: (n) => n.kind, wrap: true, cell: (n) => <span class="flex flex-col items-start gap-0.5"><KindPill m={n} />{kindHint[n.kind] && <span class="text-[11px] text-muted">{kindHint[n.kind]}</span>}</span> },
  { id: 'cluster', header: 'Cluster', sort: (n) => n.cluster, cell: (n) => n.cluster ? <a class="text-accent hover:underline" href={`/clusters/${n.cluster}/nodes`}>{n.cluster}</a> : <span class="text-muted">—</span> },
  { id: 'seen', header: 'Seen', sort: (n) => lastSeenOf(n), cell: (n) => <SeenAgo observed={lastSeenOf(n)} /> },
]

export function Discovery() {
  const all = machineList.value
  const available = useMemo(() => all.filter(addable), [all])
  const waiting = useMemo(() => all.filter((m) => m.kind === 'maintenance' && m.declared), [all])
  const rest = useMemo(() => all.filter((m) => m.kind !== 'maintenance'), [all])
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const picked = available.filter((m) => selected.has(m.mac))
  const toggle = (mac: string) => { const next = new Set(selected); if (!next.delete(mac)) next.add(mac); setSelected(next) }
  const setAll = (on: boolean) => setSelected(on ? new Set(available.map((m) => m.mac)) : new Set())
  const columns = readyColumns(selected, toggle, available, setAll)
  return (
    <div class="p-5 flex flex-col gap-4">
      <Section title="Discovery">
        <div class="panel p-3">
          <ScanBox onError={(m) => toast(m, 'error')} />
        </div>
        <DataTable loading={!live.value} id="discovery" columns={columns} toolbar={picked.length > 0 && <AddMachines machines={picked} label={`Add ${picked.length} to cluster`} onDone={() => setSelected(new Set())} />} rows={available} rowKey={(n) => n.mac || n.ip} defaultSort={{ id: 'ip', dir: 'asc' }} empty="No machines to add." />
      </Section>
      {waiting.length > 0 && <Waiting machines={waiting} />}
      <PxePanel />
      {rest.length > 0 && (
        <Section title={`Other machines (${rest.length})`}>
          <DataTable id="machines" columns={other} rows={rest} rowKey={(n) => n.mac || n.ip} defaultSort={{ id: 'ip', dir: 'asc' }} />
        </Section>
      )}
    </div>
  )
}

function Waiting({ machines }: { machines: NodeRow[] }) {
  const byCluster = new Map<string, NodeRow[]>()
  for (const m of machines) byCluster.set(m.declared!.cluster, [...(byCluster.get(m.declared!.cluster) ?? []), m])
  return (
    <Section title="Waiting for apply">
      {[...byCluster].map(([cluster, ms]) => (
        <div key={cluster} class="panel">
          <div class="flex items-center gap-2 px-3 py-2 border-b border-border text-[13px]">
            <a class="font-medium hover:underline" href={`/clusters/${cluster}/changes`}>{cluster}</a>
            <span class="text-muted">{ms.length} machine{ms.length === 1 ? '' : 's'}</span>
            <a class="btn btn-sm btn-primary ml-auto" href={`/clusters/${cluster}/changes`}>Review changes</a>
          </div>
          <div class="divide-y divide-border/60">
            {ms.sort((a, b) => a.declared!.hostname.localeCompare(b.declared!.hostname)).map((m) => (
              <div key={m.mac} class="flex items-center gap-3 px-3 py-1.5 text-[13px]">
                <span class="mono w-40 shrink-0">{m.declared!.hostname}</span>
                <span class="mono w-32 shrink-0">{m.ip}</span>
                <span class="text-muted truncate">{modelOf(m)}</span>
                <span class="ml-auto"><SeenAgo observed={lastSeenOf(m)} /></span>
              </div>
            ))}
          </div>
        </div>
      ))}
    </Section>
  )
}

function PxePanel() {
  const { data: st } = useLive(() => api.pxe(), [], [['', 'pxe']], { onError: 'silent' })
  if (!st) return null
  if (!st.running) return <Notice tone={st.error ? 'warn' : 'muted'}><span class="flex flex-col gap-2"><span>{st.error ? `Network boot: ${st.error}` : 'Network boot is off.'} Start it with:</span><Code text={st.command ?? 'sudo kubit pxe'} /></span></Notice>
  const boots = st.boots ?? []
  return (
    <Section title="Network boot">
      <div class="panel p-3">
        <KeyValue rows={[
          ['State', <span class="flex items-center gap-2"><Pill tone="good">running since {fmt.when(st.startedAt ?? '')}</Pill>{st.httpOnly && <Pill tone="warn">HTTP only</Pill>}</span>],
          ['Interface', <span class="mono">{st.ip ? `${st.interface} (${st.ip})` : st.interface}</span>],
          ['Talos', <span class="mono">{st.talosVersion}</span>],
          ['Booted', boots.length ? boots.slice(0, 8).map((b) => <span key={b.mac} class="mono mr-3">{b.mac} {b.stage}</span>) : <span class="text-muted">none yet</span>],
        ]} />
      </div>
    </Section>
  )
}
