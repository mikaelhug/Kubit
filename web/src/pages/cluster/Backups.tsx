import { useEffect, useMemo, useState } from 'preact/hooks'
import { api, fmt, snapshotUrl, type Snapshot } from '../../api'
import { DataTable, type Column } from '../../components/DataTable'
import { RunLog } from '../../components/RunLog'
import { Ago } from '../../components/Time'
import { Code, Pill, Section, Tile } from '../../components/ui'
import { applyRuns, loadApplyRun, toast } from '../../store'
import { useLive } from '../../useLive'
import type { ClusterCtx } from './ClusterPage'

export function Backups({ ctx }: { ctx: ClusterCtx }) {
  const { name, cluster } = ctx
  const { data: snaps } = useLive(() => api.snapshots(name), [name], [[name, 'snapshots']], { onError: 'silent' })
  const rows = snaps ?? []
  const b = cluster.spec.spec.backup
  const live = ctx.status?.backup
  const { data: repo } = useLive(() => api.repo(name), [name], [[name, 'repo']], { onError: 'silent' })
  const run = applyRuns.value.get(name)
  const taking = !!run?.running && run.kind === 'snapshot'
  const [hidden, setHidden] = useState<string | undefined>()
  useEffect(() => { loadApplyRun(name) }, [name])
  const take = () => { api.takeSnapshot(name).catch((e) => toast(e.message, 'error')) }
  const dir = repo?.dir ?? name
  const latest = rows[0]
  const columns = useMemo<Column<Snapshot>[]>(() => [
    { id: 'ts', header: 'Taken', sort: (s) => s.ts, cell: (s) => fmt.datetime(s.ts) },
    { id: 'source', header: 'Source', sort: (s) => s.source, cell: (s) => <Pill tone="info">{s.source}</Pill> },
    { id: 'id', header: 'ID', mono: true, sort: (s) => s.id, cell: (s) => s.id },
    { id: 'size', header: 'Size', align: 'right', sort: (s) => s.sizeBytes, cell: (s) => fmt.bytes(s.sizeBytes) },
    { id: 'download', header: '', align: 'right', cell: (s) => <a class="btn btn-sm" href={snapshotUrl(name, s.id)} download>Download</a> },
  ], [name])
  return (
    <Section title="etcd snapshots"
      actions={<button class="btn btn-primary btn-sm" disabled={!!run?.running || !ctx.status?.apiReachable} onClick={take}>{taking ? 'Taking snapshot' : 'Take snapshot'}</button>}>
      <div class="grid grid-cols-1 md:grid-cols-3 gap-3">
        <Tile size="xl" label="Latest snapshot" value={latest ? <Ago iso={latest.ts} /> : 'none'} title={latest ? fmt.datetime(latest.ts) : undefined} sub={latest ? fmt.bytes(latest.sizeBytes) : undefined} tone={latest ? undefined : 'warn'} />
        <Tile size="xl" label="talos-backup" value={live ? live.schedule : 'off'} sub={!live ? undefined : live.failed ? 'last run failed' : live.lastSuccess ? `last success ${fmt.datetime(live.lastSuccess)}` : 'no run yet'} title={b?.s3 ? `s3://${b.s3.bucket}/${b.s3.prefix || cluster.name}` : undefined} tone={!live ? 'muted' : live.failed ? 'bad' : undefined} pending={!!ctx.status?.apiReachable && (b?.schedule ?? '') !== (live?.schedule ?? '')} />
        <Tile size="xl" label="Stored" value={String(rows.length)} sub={rows.length ? fmt.bytes(rows.reduce((a, r) => a + r.sizeBytes, 0)) : undefined} />
      </div>
      {run?.kind === 'snapshot' && (run.running || run.error) && run.started !== hidden && <RunLog run={run} capped onClose={() => setHidden(run.started)} />}
      <DataTable loading={!snaps} id="snapshots" columns={columns} rows={rows} rowKey={(s) => s.id} defaultSort={{ id: 'ts', dir: 'desc' }} empty="No snapshots yet." />
      <div class="flex flex-col gap-1"><span class="label">Restore</span><Code text={`kubit etcd restore ${dir} <id>`} /></div>
    </Section>
  )
}
