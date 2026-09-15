import { useEffect } from 'preact/hooks'
import { fmt, type AuditEntry } from '../api'
import { audit, loadAudit, refreshKey } from '../store'
import { DataTable, type Column } from './DataTable'
import { Section } from './ui'

/** Who did what, when: every administrative action Kubit recorded. */
export function AuditLog({ cluster }: { cluster?: string }) {
  const rows = cluster ? audit.value.filter((a) => a.cluster === cluster) : audit.value
  useEffect(() => { loadAudit(cluster) }, [cluster, refreshKey('*', 'resync')])
  const columns: Column<AuditEntry>[] = [
    { id: 'at', header: 'When', sort: (a) => a.at, cell: (a) => <span class="num text-muted">{fmt.datetime(a.at)}</span> },
    ...(cluster ? [] : [{ id: 'cluster', header: 'Cluster', sort: (a: AuditEntry) => a.cluster, cell: (a: AuditEntry) => a.cluster ? <a href={`/clusters/${a.cluster}/overview`} class="text-accent hover:underline">{a.cluster}</a> : <span class="text-muted">kubit</span> } as Column<AuditEntry>]),
    { id: 'action', header: 'Action', sort: (a) => a.action, mono: true, cell: (a) => a.action },
    { id: 'detail', header: 'Detail', text: (a) => a.detail, cell: (a) => <span class="mono text-[11px] text-muted break-all">{a.detail.length > 160 ? a.detail.slice(0, 160) + '…' : a.detail}</span> },
  ]
  const csv = () => {
    const lines = [['at', 'cluster', 'action', 'detail'].join(','), ...rows.map((a) => [a.at, a.cluster, a.action, JSON.stringify(a.detail)].join(','))]
    const url = URL.createObjectURL(new Blob([lines.join('\n')], { type: 'text/csv' }))
    const el = document.createElement('a'); el.href = url; el.download = `kubit-audit${cluster ? '-' + cluster : ''}.csv`; el.click(); URL.revokeObjectURL(url)
  }
  return (
    <Section title="Audit log" help="Administrative actions in order: cluster creation, node changes, upgrades, snapshots, restores, credential rotation, settings changes. The single-admin deployment records no user; add a note in your change tracker when several people share this host." actions={<button class="btn" onClick={csv} disabled={rows.length === 0}>Export CSV</button>}>
      <DataTable id={`audit-${cluster ?? 'all'}`} columns={columns} rows={rows} rowKey={(a) => String(a.id)} defaultSort={{ id: 'at', dir: 'desc' }} empty="Nothing recorded yet." />
    </Section>
  )
}
