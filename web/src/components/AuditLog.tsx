import { useEffect, useMemo } from 'preact/hooks'
import { fmt, type AuditEntry } from '../api'
import { audit, loadAudit, refreshKey } from '../store'
import { DataTable, type Column } from './DataTable'

export function AuditLog({ cluster }: { cluster?: string }) {
  const all = audit.value
  const rows = useMemo(() => cluster ? all.filter((a) => a.cluster === cluster) : all, [all, cluster])
  useEffect(() => { loadAudit(cluster) }, [cluster, refreshKey('*', 'resync')])
  const columns = useMemo<Column<AuditEntry>[]>(() => [
    { id: 'at', header: 'When', sort: (a) => a.at, cell: (a) => <span class="text-muted">{fmt.datetime(a.at)}</span> },
    ...(cluster ? [] : [{ id: 'cluster', header: 'Cluster', sort: (a: AuditEntry) => a.cluster, cell: (a: AuditEntry) => a.cluster ? <a href={`/clusters/${a.cluster}/overview`} class="text-accent hover:underline">{a.cluster}</a> : <span class="text-muted">kubit</span> } as Column<AuditEntry>]),
    { id: 'actor', header: 'Who', sort: (a) => a.actor ?? '', cell: (a) => a.actor || <span class="text-muted">—</span> },
    { id: 'action', header: 'Action', sort: (a) => a.action, mono: true, cell: (a) => a.action },
    { id: 'detail', header: 'Detail', text: (a) => a.detail, cell: (a) => <span class="mono text-[11px] text-muted block max-w-[640px] truncate" title={a.detail}>{a.detail}</span> },
  ], [cluster])
  const csv = () => {
    const lines = [['at', 'actor', 'cluster', 'action', 'detail'].join(','), ...rows.map((a) => [a.at, a.actor ?? '', a.cluster, a.action, JSON.stringify(a.detail)].join(','))]
    const url = URL.createObjectURL(new Blob([lines.join('\n')], { type: 'text/csv' }))
    const el = document.createElement('a'); el.href = url; el.download = `kubit-audit${cluster ? '-' + cluster : ''}.csv`; el.click(); URL.revokeObjectURL(url)
  }
  return <DataTable id={`audit-${cluster ?? 'all'}`} columns={columns} rows={rows} rowKey={(a) => String(a.id)} defaultSort={{ id: 'at', dir: 'desc' }} empty="Nothing recorded yet." toolbar={<button class="btn btn-sm" onClick={csv} disabled={rows.length === 0}>Export CSV</button>} />
}
