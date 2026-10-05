import { useState } from 'preact/hooks'
import { api, fmt, type LabDisk, type NodeRow } from '../../api'
import { toast } from '../../store'
import { DataTable, type Column } from '../DataTable'
import { ConfirmDialog, Meter, Pill, Section } from '../ui'

export function HostStorage({ host, busy }: { host: NodeRow; busy: boolean }) {
  const lh = host.labhost!
  const [release, setRelease] = useState<LabDisk | null>(null)
  const pools = lh.capacity.pools ?? []
  const usage = lh.metrics?.pools ?? []
  const vms = lh.vms ?? []
  const poolOf = (d: LabDisk) => (d.use === 'os' ? 'system' : d.pool)
  const imagesOn = (d: LabDisk) => vms.filter((v) => (v.disks ?? []).some((x) => !x.device && (x.pool || 'system') === poolOf(d))).map((v) => v.name)
  const ownerOf = (d: LabDisk) => vms.find((v) => (v.disks ?? []).some((x) => x.device === d.id))?.name
  const space = (d: LabDisk) => {
    const name = poolOf(d)
    const u = usage.find((x) => x.name === name)
    const p = pools.find((x) => x.name === name)
    if (u?.mounted && u.total) return <Meter label="Used" used={u.used} cap={u.total} format={fmt.bytes} />
    if (p?.mounted && p.sizeBytes) return <span>{fmt.bytes(p.freeBytes)} free</span>
    return <span class="text-muted">—</span>
  }
  const use = (d: LabDisk) => {
    const owner = ownerOf(d)
    if (d.use === 'os') return <Pill tone="info">Debian · VM images</Pill>
    if (owner) return <Pill tone="info">Whole disk · {owner}</Pill>
    if (d.use === 'pool') return pools.find((p) => p.name === d.pool)?.mounted ? <Pill tone="good">VM images</Pill> : <Pill tone="bad">Not mounted</Pill>
    if (d.use === 'busy') return <Pill tone="warn" title={d.signature}>In use</Pill>
    return <Pill tone="muted" title={d.signature ? `Has ${d.signature}; wiped when used` : undefined}>Free</Pill>
  }
  const columns: Column<LabDisk>[] = [
    { id: 'dev', header: 'Disk', sort: (d) => d.devPath ?? d.key, cell: (d) => (
      <span class="flex flex-col">
        <span class="mono">{(d.devPath ?? d.key).replace(/^\/dev\//, '')}{d.model ? <span class="text-muted"> · {d.model}</span> : null}</span>
        <span class="text-[10px] text-muted mono truncate max-w-[22rem]" title={d.id}>{d.id}</span>
      </span>
    ) },
    { id: 'size', header: 'Size', align: 'right', sort: (d) => d.sizeBytes, cell: (d) => fmt.bytes(d.sizeBytes) },
    { id: 'use', header: 'Use', cell: use },
    { id: 'space', header: 'Space', width: '14rem', cell: space },
    { id: 'vms', header: 'VMs', wrap: true, cell: (d) => { const names = ownerOf(d) ? [ownerOf(d)!] : imagesOn(d); return names.length ? <span class="text-[12px]">{names.join(', ')}</span> : <span class="text-muted">—</span> } },
    { id: 'actions', header: '', align: 'right', cell: (d) => d.use === 'pool' && imagesOn(d).length === 0 ? <button class="btn btn-sm" disabled={busy} onClick={() => setRelease(d)}>Release</button> : null },
  ]
  const name = (d: LabDisk) => (d.devPath ?? d.key).replace(/^\/dev\//, '')
  return (
    <Section title="Disks" help="Images share a disk; a whole disk belongs to one VM.">
      <DataTable id="labhost-disks" search={false} columns={columns} rows={lh.capacity.disks ?? []} rowKey={(d) => d.key} empty="No disks reported yet." />
      {release && (
        <ConfirmDialog title={`Release ${name(release)}`} action="Release" tone="danger" onClose={() => setRelease(null)}
          onConfirm={() => api.labPoolRelease(host.mac, release.pool!).then(() => setRelease(null)).catch((e) => toast(e.message, 'error'))}
          impact={<p>Unmounts and wipes {name(release)}.</p>} />
      )}
    </Section>
  )
}
