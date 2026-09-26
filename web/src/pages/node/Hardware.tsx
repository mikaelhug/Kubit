import { fmt, type Inventory, type NodeRow } from '../../api'
import { DataTable, type Column } from '../../components/DataTable'
import { Notice, Pill, Section, Tile } from '../../components/ui'
import { modelOf } from '../../machine'

type Disk = Inventory['disks'][number]
type Link = Inventory['links'][number]

const diskColumns: Column<Disk>[] = [
  { id: 'dev', header: 'Device', mono: true, sort: (d) => d.devPath, cell: (d) => d.devPath || <span class="text-muted">—</span> },
  { id: 'size', header: 'Size', align: 'right', sort: (d) => d.sizeBytes, cell: (d) => fmt.bytes(d.sizeBytes) },
  { id: 'model', header: 'Model', sort: (d) => d.model ?? '', cell: (d) => d.model || <span class="text-muted">—</span> },
  { id: 'transport', header: 'Transport', sort: (d) => d.transport ?? '', cell: (d) => d.transport || '—' },
  { id: 'type', header: 'Type', cell: (d) => d.cdrom ? 'cdrom' : d.rotational ? 'HDD' : 'SSD/flash' },
  { id: 'flags', header: '', cell: (d) => d.readonly ? <Pill tone="muted">read-only</Pill> : null },
]

const linkColumns: Column<Link>[] = [
  { id: 'name', header: 'Interface', mono: true, sort: (l) => l.name, cell: (l) => l.name },
  { id: 'mac', header: 'MAC', mono: true, cell: (l) => l.mac },
  { id: 'state', header: 'State', cell: (l) => <Pill tone={l.up ? 'good' : 'muted'}>{l.up ? 'up' : 'down'}</Pill> },
  { id: 'addr', header: 'Addresses', mono: true, cell: (l) => (l.addresses ?? []).join(', ') || '—' },
]

export function HardwareTab({ inv: live, invErr, node }: { inv: Inventory | null; invErr: string | null; node: NodeRow | null }) {
  if (!node) return <div class="text-muted">Loading</div>
  const inv: Inventory | null = live ?? node.inventory ?? null
  const stored = !live && !!node.inventory
  if (node.talos && !inv && !invErr) return <div class="text-muted">Loading</div>
  const note = invErr ? `${invErr}; showing the record from ${fmt.when(node.lastSeen)}.`
    : stored && inv?.disks.some((d) => !d.devPath) ? `Reported by the ${node.oobType === 'redfish' ? 'BMC' : 'management engine'}; device names arrive when Talos boots.`
    : stored ? `Recorded ${fmt.when(node.lastSeen)}; not running Talos now.`
    : ''
  if (!inv || (!inv.cpus && inv.disks.length === 0)) {
    return <Notice tone="muted">{inv ? `${modelOf(node)}. ` : ''}Hardware details arrive when the machine boots Talos.</Notice>
  }
  return (
    <div class="flex flex-col gap-5">
      {note && <Notice tone={invErr ? 'bad' : 'muted'}>{note}</Notice>}
      <div class="grid grid-cols-2 lg:grid-cols-4 gap-4">
        <Tile label="CPUs" value={String(inv.cpus)} sub={inv.arch} />
        <Tile label="Memory" value={fmt.bytes(inv.memoryBytes)} />
        <Tile label="KVM" value={inv.kvm ? 'available' : 'absent'} sub={inv.kvm ? 'runsc-kvm eligible' : 'gVisor uses systrap'} />
        <Tile label="Disks" value={String(inv.disks.length)} sub={fmt.bytes(inv.disks.reduce((a, d) => a + d.sizeBytes, 0)) + ' total'} />
      </div>
      <Section title="Disks">
        <DataTable search={false} columns={diskColumns} rows={inv.disks} rowKey={(d) => d.devPath} />
      </Section>
      <Section title="Network links" help="Physical links only.">
        <DataTable search={false} columns={linkColumns} rows={inv.links} rowKey={(l) => l.name} />
      </Section>
    </div>
  )
}
