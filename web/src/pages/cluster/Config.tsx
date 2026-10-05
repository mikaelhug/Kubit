import { api, fmt, kubeconfigUrl, type CertInfo } from '../../api'
import { DataTable, type Column } from '../../components/DataTable'
import { CopyButton, ErrorBox, Pill, Section } from '../../components/ui'
import { useLive } from '../../useLive'
import type { ClusterCtx } from './ClusterPage'

const certColumns: Column<CertInfo>[] = [
  { id: 'name', header: 'Certificate', sort: (c) => c.name, cell: (c) => <span class="flex flex-col"><span class="font-medium">{c.name}</span><span class="text-[11px] text-muted mono truncate">{c.subject}</span></span> },
  { id: 'expires', header: 'Expires', sort: (c) => c.notAfter, cell: (c) => c.error ? <span class="text-bad">{c.error}</span> : fmt.date(c.notAfter) },
  { id: 'left', header: 'Left', align: 'right', sort: (c) => c.daysLeft, cell: (c) => <Pill tone={c.daysLeft < 14 ? 'bad' : c.daysLeft < 60 ? 'warn' : 'good'}>{c.daysLeft} d</Pill> },
]

export function Config({ ctx }: { ctx: ClusterCtx }) {
  const { name, cluster } = ctx
  const { data: yaml, error } = useLive(() => api.clusterYaml(name), [name], [], { refresh: [cluster.updatedAt] })
  const { data: certs } = useLive(() => api.certificates(name), [name], [[name, 'certificates']], { onError: 'null' })
  return (
    <>
      <Section title="cluster.yaml" help="Edit it in the repo, then run kubit apply."
        actions={<span class="flex items-center gap-2"><span class="text-[12px] text-muted">changed {fmt.when(cluster.updatedAt)}</span>{yaml && <CopyButton text={yaml} className="btn btn-sm" />}<a class="btn btn-sm" href={kubeconfigUrl(name)} download="kubeconfig">kubeconfig</a></span>}>
        <ErrorBox error={error} />
        <pre class="log !max-h-[60vh]">{yaml ?? ''}</pre>
      </Section>
      {certs && certs.length > 0 && (
        <Section title="Certificates">
          <DataTable id="certs" search={false} columns={certColumns} rows={certs} rowKey={(c) => c.name} defaultSort={{ id: 'left', dir: 'asc' }} />
        </Section>
      )}
    </>
  )
}
