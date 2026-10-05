import { useEffect, useMemo } from 'preact/hooks'
import { fmt, snapshotUrl, type Snapshot } from '../../api'
import { DataTable, type Column } from '../../components/DataTable'
import { Code, Pill, Section, Tile } from '../../components/ui'
import { loadSnapshots, snapshots } from '../../store'
import type { ClusterCtx } from './ClusterPage'

export function Backups({ ctx }: { ctx: ClusterCtx }) {
  const { name, cluster } = ctx
  const rows = snapshots.value.get(name) ?? []
  const loaded = snapshots.value.has(name)
  const b = cluster.spec.spec.backup
  useEffect(() => { loadSnapshots(name) }, [name])
  const latest = rows.find((r) => r.status === 'ok')
  const columns = useMemo<Column<Snapshot>[]>(() => [
    { id: 'ts', header: 'Taken', sort: (s) => s.ts, cell: (s) => fmt.datetime(s.ts) },
    { id: 'source', header: 'Source', sort: (s) => s.source, cell: (s) => <Pill tone={s.source === 'schedule' ? 'muted' : 'info'}>{s.source}</Pill> },
    { id: 'node', header: 'From', sort: (s) => s.node, mono: true, cell: (s) => s.node },
    { id: 'keys', header: 'Keys', align: 'right', sort: (s) => s.keys, cell: (s) => fmt.int(s.keys) },
    { id: 'size', header: 'Size', align: 'right', sort: (s) => s.sizeBytes, cell: (s) => fmt.bytes(s.sizeBytes) },
    { id: 'versions', header: 'Versions', mono: true, cell: (s) => <span class="text-muted text-[11px]">{s.talosVersion} · {s.k8sVersion}</span> },
    { id: 'status', header: 'Status', sort: (s) => s.status, cell: (s) => <Pill tone={s.status === 'ok' ? 'good' : 'bad'}>{s.status}</Pill> },
    { id: 'download', header: '', align: 'right', cell: (s) => <a class="btn btn-sm" href={snapshotUrl(name, s.id)} download>Download</a> },
  ], [name])
  return (
    <Section title="etcd snapshots" help="talos-backup writes encrypted snapshots to S3; the list below holds local ones taken with kubit etcd snapshot.">
      <div class="grid grid-cols-1 md:grid-cols-3 gap-3">
        <Tile size="xl" label="Latest snapshot" value={latest ? fmt.when(latest.ts) : 'none'} sub={latest ? `${fmt.int(latest.keys)} keys · ${fmt.bytes(latest.sizeBytes)}` : undefined} tone={latest ? undefined : 'warn'} />
        <Tile size="xl" label="talos-backup" value={b?.schedule ? b.schedule : 'off'} sub={b?.schedule ? `s3://${b.s3?.bucket}/${b.s3?.prefix || cluster.name}` : 'declare spec.backup'} tone={b?.schedule ? undefined : 'muted'} />
        <Tile size="xl" label="Stored" value={String(rows.length)} sub={fmt.bytes(rows.reduce((a, r) => a + r.sizeBytes, 0))} />
      </div>
      <DataTable loading={!loaded} id="snapshots" columns={columns} rows={rows} rowKey={(s) => String(s.id)} defaultSort={{ id: 'ts', dir: 'desc' }} empty="No snapshots yet." />
      <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
        <div class="flex flex-col gap-1"><span class="label">Take one</span><Code text={`kubit etcd snapshot ${name}`} /></div>
        <div class="flex flex-col gap-1"><span class="label">Restore</span><Code text={`kubit etcd restore ${name} <id>`} /></div>
      </div>
    </Section>
  )
}
