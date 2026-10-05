import { useMemo } from 'preact/hooks'
import { api, fmt, type NodeRow } from '../api'
import { DataTable, type Column } from '../components/DataTable'
import { KindPill, TypePill } from '../components/Machine'
import { ScanBox } from '../components/ScanBox'
import { Code, CopyButton, KeyValue, Notice, Pill, Section } from '../components/ui'
import { installCandidates, lastSeenOf, modelOf, nodeEntry } from '../machine'
import { live, machineList, toast, versions } from '../store'
import { useLive } from '../useLive'
import { defaultTalos, talosIso } from '../versions'

const factory = 'https://factory.talos.dev'

const hardware = (n: NodeRow) => {
  const inv = n.inventory
  if (!inv) return <span class="text-muted">—</span>
  const disks = installCandidates(n)
  return (
    <span class="whitespace-nowrap" title={disks.map((d) => `${d.devPath} ${fmt.bytes(d.sizeBytes)}${d.rotational ? ' HDD' : ''}`).join(', ')}>
      {inv.cpus} CPU · {fmt.bytes(inv.memoryBytes)} · {disks.length ? `${fmt.bytes(disks[0].sizeBytes)}${disks.length > 1 ? ` +${disks.length - 1}` : ''}` : 'no disk'}
      <span class="block text-[10px] text-muted">{inv.arch || n.arch}{inv.kvm ? ' · kvm' : ''}{inv.tpm ? ' · tpm' : ''}</span>
    </span>
  )
}

const machineCell = (n: NodeRow) => (
  <a href={`/machines/${n.mac}`} class="flex flex-col min-w-0 hover:underline">
    <span class="font-medium truncate">{n.inventory?.hostname || modelOf(n)}</span>
    <span class="text-[10px] text-muted mono truncate">{n.mac}</span>
  </a>
)

const ready: Column<NodeRow>[] = [
  { id: 'machine', header: 'Machine', sort: (n) => n.ip, text: (n) => `${modelOf(n)} ${n.mac} ${n.serial ?? ''}`, cell: machineCell },
  { id: 'ip', header: 'Address', mono: true, sort: (n) => n.ip, cell: (n) => n.ip },
  { id: 'type', header: 'Type', cell: (n) => <TypePill m={n} /> },
  { id: 'hardware', header: 'Hardware', sort: (n) => n.inventory?.memoryBytes ?? 0, cell: hardware },
  { id: 'talos', header: 'Talos', mono: true, cell: (n) => n.inventory?.talosVersion || n.talosVersion || '—' },
  { id: 'seen', header: 'Seen', sort: (n) => lastSeenOf(n), cell: (n) => <span class="text-muted">{fmt.when(lastSeenOf(n))}</span> },
  { id: 'entry', header: '', align: 'right', cell: (n) => <CopyButton className="btn btn-primary btn-sm" label="Copy node entry" text={() => nodeEntry(n)} /> },
]

const other: Column<NodeRow>[] = [
  { id: 'machine', header: 'Machine', sort: (n) => n.ip, text: (n) => `${modelOf(n)} ${n.mac}`, cell: machineCell },
  { id: 'ip', header: 'Address', mono: true, sort: (n) => n.ip, cell: (n) => n.ip || '—' },
  { id: 'state', header: 'State', sort: (n) => n.kind, cell: (n) => <KindPill m={n} /> },
  { id: 'cluster', header: 'Cluster', sort: (n) => n.cluster, cell: (n) => n.cluster ? <a class="text-accent hover:underline" href={`/clusters/${n.cluster}/nodes`}>{n.cluster}</a> : <span class="text-muted">—</span> },
  { id: 'seen', header: 'Seen', sort: (n) => lastSeenOf(n), cell: (n) => <span class="text-muted">{fmt.when(lastSeenOf(n))}</span> },
]

export function Discovery() {
  const all = machineList.value
  const available = useMemo(() => all.filter((m) => m.kind === 'maintenance'), [all])
  const rest = useMemo(() => all.filter((m) => m.kind !== 'maintenance'), [all])
  const talos = defaultTalos(versions.value)
  return (
    <div class="p-5 flex flex-col gap-4">
      <Section title="Discovery" help="Talos machines in maintenance mode. Copy an entry into cluster.yaml, then run kubit apply.">
        <div class="panel p-3 flex flex-col gap-2">
          <ScanBox primary fallbackIp={all[0]?.ip} onError={(m) => toast(m, 'error')} />
          <div class="text-[12px] text-muted">Talos ISO: <a class="text-accent hover:underline" href={talosIso(factory, talos, 'amd64')}>amd64</a> · <a class="text-accent hover:underline" href={talosIso(factory, talos, 'arm64')}>arm64</a></div>
        </div>
        <DataTable loading={!live.value} id="discovery" columns={ready} rows={available} rowKey={(n) => n.mac || n.ip} defaultSort={{ id: 'ip', dir: 'asc' }} empty="No machines in maintenance mode." />
      </Section>
      <PxePanel />
      {rest.length > 0 && (
        <Section title={`Other machines (${rest.length})`}>
          <DataTable id="machines" columns={other} rows={rest} rowKey={(n) => n.mac || n.ip} defaultSort={{ id: 'ip', dir: 'asc' }} />
        </Section>
      )}
    </div>
  )
}

function PxePanel() {
  const { data: st } = useLive(() => api.pxe(), [], [['', 'pxe']], { onError: 'silent' })
  if (!st) return null
  if (!st.running) return <Notice tone="muted"><span class="flex flex-col gap-2"><span>Network boot is off. Start it with:</span><Code text={st.command ?? 'sudo kubit pxe'} /></span></Notice>
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
