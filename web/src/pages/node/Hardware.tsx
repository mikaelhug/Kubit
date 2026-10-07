import type { ComponentChildren } from 'preact'
import { fmt, type Inventory, type NodeRow } from '../../api'
import { DataTable, type Column } from '../../components/DataTable'
import { KeyValue, Notice, Pill, Section, Tile } from '../../components/ui'
import { memoryOf, modelName } from '../../machine'

type Disk = Inventory['disks'][number]
type Link = Inventory['links'][number]
type Dimm = NonNullable<Inventory['memory']>[number]
type Pci = NonNullable<Inventory['pci']>[number]

const or = (v?: ComponentChildren) => v || <span class="text-muted">—</span>
const mts = (n?: number) => (n ? `${n} MT/s` : '')
const linkSpeed = (mbps?: number) => (!mbps ? '' : mbps >= 1000 ? `${mbps / 1000} Gb/s` : `${mbps} Mb/s`)
const withData = <T,>(cols: Column<T>[], rows: T[], has: Record<string, (row: T) => unknown>) =>
  cols.filter((c) => !has[c.id] || rows.some(has[c.id]))
const cpuName = (m: string) => m.replace(/\((R|TM)\)/gi, '').replace(/\s+@.*$/, '').replace(/\s+CPU\b(?!\s+version)/, '').replace(/\s+/g, ' ').trim()

const dimmColumns: Column<Dimm>[] = [
  { id: 'slot', header: 'Slot', sort: (m) => m.slot, cell: (m) => <>{m.slot || '—'}{m.bank && <span class="text-muted"> · {m.bank}</span>}</> },
  { id: 'size', header: 'Size', align: 'right', sort: (m) => m.sizeBytes, cell: (m) => m.empty ? <span class="text-muted">empty</span> : or(m.sizeBytes && fmt.bytes(m.sizeBytes)) },
  { id: 'type', header: 'Type', cell: (m) => !m.empty && or(m.type) },
  { id: 'speed', header: 'Speed', align: 'right', cell: (m) => !m.empty && or(mts(m.configuredMTs)) },
  { id: 'rated', header: 'Rated', align: 'right', cell: (m) => !m.empty && or(mts(m.speedMTs)) },
  { id: 'maker', header: 'Manufacturer', cell: (m) => !m.empty && or(m.manufacturer) },
  { id: 'part', header: 'Part number', mono: true, cell: (m) => !m.empty && or(m.part) },
  { id: 'serial', header: 'Serial', mono: true, cell: (m) => !m.empty && or(m.serial) },
]

const diskColumns: Column<Disk>[] = [
  { id: 'dev', header: 'Device', mono: true, sort: (d) => d.devPath, cell: (d) => or(d.devPath) },
  { id: 'size', header: 'Size', align: 'right', sort: (d) => d.sizeBytes, cell: (d) => fmt.bytes(d.sizeBytes) },
  { id: 'model', header: 'Model', sort: (d) => d.model ?? '', cell: (d) => or(d.model) },
  { id: 'serial', header: 'Serial', mono: true, cell: (d) => or(d.serial) },
  { id: 'firmware', header: 'Firmware', mono: true, cell: (d) => or(d.firmware) },
  { id: 'transport', header: 'Transport', sort: (d) => d.transport ?? '', cell: (d) => d.transport || '—' },
  { id: 'type', header: 'Type', cell: (d) => d.cdrom ? 'cdrom' : d.rotational ? 'HDD' : 'SSD/flash' },
  { id: 'flags', header: '', cell: (d) => d.readonly ? <Pill tone="muted">read-only</Pill> : null },
]

const linkColumns: Column<Link>[] = [
  { id: 'name', header: 'Interface', mono: true, sort: (l) => l.name, cell: (l) => l.name },
  { id: 'mac', header: 'MAC', mono: true, cell: (l) => l.mac },
  { id: 'state', header: 'State', cell: (l) => <Pill tone={l.up ? 'good' : 'muted'}>{l.up ? 'up' : 'down'}</Pill> },
  { id: 'speed', header: 'Speed', align: 'right', sort: (l) => l.speedMbps ?? 0, cell: (l) => or(linkSpeed(l.speedMbps)) },
  { id: 'mtu', header: 'MTU', align: 'right', cell: (l) => or(l.mtu) },
  { id: 'addr', header: 'Addresses', mono: true, cell: (l) => (l.addresses ?? []).join(', ') || '—' },
]

const pciColumns: Column<Pci>[] = [
  { id: 'address', header: 'Address', mono: true, sort: (p) => p.address, cell: (p) => p.address },
  { id: 'kind', header: 'Kind', sort: (p) => p.kind ?? '', cell: (p) => or(p.kind) },
  { id: 'vendor', header: 'Vendor', sort: (p) => p.vendor ?? '', cell: (p) => or(p.vendor) },
  { id: 'product', header: 'Product', wrap: true, cell: (p) => or(p.product) },
  { id: 'driver', header: 'Driver', mono: true, cell: (p) => or(p.driver) },
]

function cpuTile(inv: Inventory) {
  if (!inv.cpuModel) return <Tile label="CPUs" value={String(inv.cpus)} sub={inv.arch} />
  const sockets = inv.cpuSockets && inv.cpuSockets > 1 ? `${inv.cpuSockets} sockets` : ''
  const threads = inv.cpuCores ? `${inv.cpus} threads` : ''
  const sub = [cpuName(inv.cpuModel), threads, sockets].filter(Boolean).join(' · ')
  return <Tile label={inv.cpuCores ? 'CPU cores' : 'CPUs'} value={String(inv.cpuCores || inv.cpus)} sub={sub} />
}

function memoryTile(inv: Inventory, dimms: Dimm[]) {
  const used = dimms.filter((m) => !m.empty)
  const type = used.find((m) => m.type)?.type
  const speed = Math.min(...used.map((m) => m.configuredMTs || m.speedMTs || Infinity))
  const kind = type && isFinite(speed) ? `${type}-${speed}` : type || mts(isFinite(speed) ? speed : 0)
  const slots = dimms.length > 1 ? `${used.length} of ${dimms.length} slots` : ''
  return <Tile label="Memory" value={fmt.bytes(memoryOf(inv))} sub={[kind, slots].filter(Boolean).join(' · ') || undefined} />
}

export function HardwareTab({ inv: live, invErr, node }: { inv: Inventory | null; invErr: string | null; node: NodeRow | null }) {
  if (!node) return <div class="text-muted">Loading</div>
  const found: Inventory | null = live ?? node.inventory ?? null
  const inv = found && { ...found, disks: found.disks ?? [], links: found.links ?? [] }
  const stored = !live && !!node.inventory
  if (node.talos && !inv && !invErr) return <div class="text-muted">Loading</div>
  const note = invErr ? `${invErr}; showing the record from ${fmt.when(node.lastSeen)}.`
    : stored ? `Recorded ${fmt.when(node.lastSeen)}; not running Talos now.`
    : ''
  if (!inv || (!inv.cpus && inv.disks.length === 0)) {
    return <Notice tone="muted">{inv ? `${modelName(inv)}. ` : ''}No hardware details yet.</Notice>
  }
  const dimms = inv.memory ?? []
  const firmware: [string, ComponentChildren][] = [['BIOS', inv.biosVersion], ['System version', inv.systemVersion]].filter((r): r is [string, string] => !!r[1])
  return (
    <div class="flex flex-col gap-5">
      {note && <Notice tone={invErr ? 'bad' : 'muted'}>{note}</Notice>}
      <div class="grid grid-cols-2 lg:grid-cols-4 gap-4">
        {cpuTile(inv)}
        {memoryTile(inv, dimms)}
        <Tile label="KVM" value={inv.kvm ? 'available' : 'absent'} />
        <Tile label="Disks" value={String(inv.disks.length)} sub={fmt.bytes(inv.disks.reduce((a, d) => a + d.sizeBytes, 0)) + ' total'} />
      </div>
      {dimms.length > 0 && (
        <Section title="Memory">
          <DataTable search={false} columns={withData(dimmColumns, dimms, { type: (m) => m.type, speed: (m) => m.configuredMTs, rated: (m) => m.speedMTs && m.speedMTs !== m.configuredMTs, part: (m) => m.part, serial: (m) => m.serial })} rows={dimms} rowKey={(m) => String(dimms.indexOf(m))} />
        </Section>
      )}
      <Section title="Disks">
        <DataTable search={false} columns={withData(diskColumns, inv.disks, { serial: (d) => d.serial, firmware: (d) => d.firmware })} rows={inv.disks} rowKey={(d) => d.devPath} />
      </Section>
      <Section title="Network links">
        <DataTable search={false} columns={withData(linkColumns, inv.links, { speed: (l) => l.speedMbps })} rows={inv.links} rowKey={(l) => l.name} />
      </Section>
      {!!inv.pci?.length && (
        <Section title="PCI devices">
          <DataTable search={false} columns={pciColumns} rows={inv.pci} rowKey={(p) => p.address} />
        </Section>
      )}
      {firmware.length > 0 && (
        <Section title="Firmware">
          <div class="panel p-3"><KeyValue rows={firmware} /></div>
        </Section>
      )}
    </div>
  )
}
