import { useEffect, useMemo } from 'preact/hooks'
import { fmt, type NodeRow } from '../../api'
import { nowEvery } from '../../clock'
import { DataTable, type Column } from '../../components/DataTable'
import { ScanBox } from '../../components/ScanBox'
import { Field } from '../../components/ui'
import { installCandidates, modelOf, TypePill } from '../../machine'
import { machineList, running } from '../../store'
import { machineWarnings, topologyText, type Draft, type PatchDraft } from './draft'

type Row = { m: NodeRow; stale: boolean }

export function MachinesStep({ draft, patch, setError }: { draft: Draft; patch: PatchDraft; setError: (e: string | null) => void }) {
  const t = nowEvery(15_000)
  const fresh = (n: NodeRow) => (t - Date.parse(n.lastSeen)) / 1000 < 3 * 60
  const all = machineList.value.filter((n) => n.state === 'maintenance' && !n.cluster)
  const free = all.filter(fresh)
  const stale = all.filter((n) => !fresh(n))
  const freeKey = free.map((m) => m.mac + m.ip + m.lastSeen).join('|')
  useEffect(() => { patch({ machines: free, selected: draft.selected.filter((mac) => free.some((m) => m.mac === mac)) }) }, [freeKey])
  const toggle = (mac: string) => patch({ selected: draft.selected.includes(mac) ? draft.selected.filter((x) => x !== mac) : [...draft.selected, mac] })
  const chosen = draft.machines.filter((m) => draft.selected.includes(m.mac))
  const columns = useMemo<Column<Row>[]>(() => [
    { id: 'pick', header: <input type="checkbox" checked={draft.machines.length > 0 && chosen.length === draft.machines.length} onChange={(e) => patch({ selected: (e.target as HTMLInputElement).checked ? draft.machines.map((m) => m.mac) : [] })} aria-label="Select all" />, width: '2rem', cell: ({ m, stale }) => stale ? <input type="checkbox" disabled /> : <input type="checkbox" class="pointer-events-none" checked={draft.selected.includes(m.mac)} readOnly /> },
    { id: 'machine', header: 'Machine', cell: ({ m, stale }) => (
      <span class="flex flex-col min-w-0">
        <span class="flex items-center gap-2"><span class="font-medium">{modelOf(m)}</span>{!stale && <TypePill m={m} />}</span>
        <span class="text-[10px] text-muted mono">{stale ? m.mac : `${m.serial ? `${m.serial} · ` : ''}${m.mac} · Talos ${m.talosVersion}`}</span>
      </span>
    ) },
    { id: 'ip', header: 'Address', mono: true, cell: ({ m }) => m.ip },
    { id: 'resources', header: 'Resources', cell: ({ m, stale }) => stale ? null : (
      <span class="flex flex-col">
        <span>{m.arch} · {m.inventory?.cpus ?? '?'} CPU · {fmt.bytes(m.inventory?.memoryBytes ?? 0)}{m.inventory?.kvm && <span class="text-[10px] text-muted"> · kvm</span>}</span>
        <span class="text-[10px] text-muted">{m.inventory?.links?.length ?? 0} NIC{(m.inventory?.links?.length ?? 0) === 1 ? '' : 's'}, {m.inventory?.links?.filter((l) => l.up).length ?? 0} up</span>
      </span>
    ) },
    { id: 'disk', header: 'Install disk', mono: true, cell: ({ m, stale }) => { if (stale) return null; const disks = installCandidates(m); return disks[0] ? (
      <span class="flex flex-col">
        <span>{disks[0].devPath} {fmt.bytes(disks[0].sizeBytes)}</span>
        <span class="text-[10px] text-muted">{disks[0].transport ?? ''}{disks[0].rotational && disks[0].transport !== 'virtio' ? ' hdd' : ''}{disks.length > 1 ? ` +${disks.length - 1} more` : ''}</span>
      </span>
    ) : <span class="text-bad">none</span> } },
    { id: 'notes', header: 'Notes', wrap: true, cell: ({ m, stale }) => { if (stale) return <span class="text-[12px] text-muted">not answering, last seen {fmt.when(m.lastSeen)}</span>; const warns = machineWarnings(m, chosen.length ? chosen : draft.machines); return warns.length ? <span class="text-warn text-[12px]">{warns.join('; ')}</span> : <span class="text-muted">—</span> } },
  ], [draft.machines, draft.selected])
  return (
    <>
      <div class="panel p-3 flex flex-col gap-3">
        <p class="text-[13px] text-muted">Machines in Talos maintenance mode; boot media and remote management are in <a class="text-accent hover:underline" href="/fleet/inventory">Inventory</a>.</p>
        <ScanBox fallbackIp={free[0]?.ip} onError={setError} />
      </div>
      <DataTable search={false} columns={columns} rows={[...draft.machines.map((m) => ({ m, stale: false })), ...stale.map((m) => ({ m, stale: true }))]} rowKey={(r) => r.m.mac}
        onRowClick={(r) => { if (!r.stale) toggle(r.m.mac) }} rowClass={(r) => (r.stale ? 'opacity-50 !cursor-default' : '')}
        empty={running.value.some((o) => o.kind === 'discover') ? 'Scanning' : 'No machines in maintenance mode; boot one from a Talos ISO and scan its subnet.'} />
      <HiddenMachinesNote />
      <div class="flex items-end gap-3">
        <Field label="Cluster name" hint="DNS label; prefixes hostnames and the kubeconfig context">
          <input class="input w-56 mono" value={draft.name} onInput={(e) => patch({ name: (e.target as HTMLInputElement).value.toLowerCase() })} />
        </Field>
        <span class="text-[13px] text-muted pb-2">{chosen.length} machine{chosen.length === 1 ? '' : 's'} selected · {topologyText(chosen.length)}</span>
      </div>
    </>
  )
}

function HiddenMachinesNote() {
  const all = machineList.value.filter((m) => !m.host)
  const members = all.filter((m) => m.kind === 'member').length
  const hosts = all.filter((m) => m.kind === 'labhost').length
  const boot = all.filter((m) => m.kind === 'unbooted' || m.kind === 'booting' || m.kind === 'configured').length
  if (members === 0 && hosts === 0 && boot === 0) return null
  const parts = [boot ? `${boot} need${boot === 1 ? 's' : ''} booting` : '', hosts ? `${hosts} ${hosts === 1 ? 'is a lab host' : 'are lab hosts'}` : '', members ? `${members} ${members === 1 ? 'is' : 'are'} in a cluster` : ''].filter(Boolean)
  return <p class="text-[12px] text-muted -mt-2">Not listed: {parts.join('; ')}. <a class="text-accent hover:underline" href={boot ? '/fleet/inventory?filter=boot' : '/fleet/inventory'}>Inventory</a></p>
}
