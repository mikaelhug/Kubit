import { useEffect, useState } from 'preact/hooks'
import { api, fmt, type StorageView } from '../../api'
import { DataTable, type Column } from '../../components/DataTable'
import { AlertPill, ErrorBox, Notice, Pill, Section } from '../../components/ui'
import { openAlert, refreshKey } from '../../store'
import type { ClusterCtx } from './ClusterPage'

type SC = StorageView['classes'][number]
type PV = StorageView['volumes'][number]
type PVC = StorageView['claims'][number]

export function Storage({ ctx }: { ctx: ClusterCtx }) {
  const { name } = ctx
  const [view, setView] = useState<StorageView | null>(null)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => { api.storage(name).then(setView).catch((e) => setError(e.message)) }, [name, refreshKey(name, 'storage')])
  const scols: Column<SC>[] = [
    { id: 'name', header: 'Class', sort: (c) => c.name, cell: (c) => <span class="font-medium">{c.name} {c.default && <Pill tone="info">default</Pill>}</span> },
    { id: 'prov', header: 'Provisioner', mono: true, cell: (c) => c.provisioner },
    { id: 'reclaim', header: 'Reclaim', cell: (c) => c.reclaim },
    { id: 'binding', header: 'Binding', cell: (c) => c.binding },
    { id: 'expand', header: 'Expandable', cell: (c) => c.expandable ? 'yes' : 'no' },
  ]
  const vcols: Column<PV>[] = [
    { id: 'name', header: 'Volume', mono: true, sort: (v) => v.name, cell: (v) => v.name },
    { id: 'cap', header: 'Capacity', align: 'right', sort: (v) => v.capacityBytes, cell: (v) => fmt.bytes(v.capacityBytes) },
    { id: 'phase', header: 'Phase', cell: (v) => <Pill tone={v.phase === 'Bound' ? 'good' : v.phase === 'Available' ? 'info' : 'warn'}>{v.phase}</Pill> },
    { id: 'class', header: 'Class', cell: (v) => v.class || '—' },
    { id: 'claim', header: 'Claim', mono: true, cell: (v) => v.claim || <span class="text-muted">—</span> },
    { id: 'modes', header: 'Access', cell: (v) => v.accessModes },
    { id: 'reclaim', header: 'Reclaim', cell: (v) => v.reclaim },
    { id: 'age', header: 'Age', cell: (v) => <span class="num text-muted">{v.age}</span> },
  ]
  const ccols: Column<PVC>[] = [
    { id: 'ns', header: 'Namespace', sort: (c) => c.namespace, cell: (c) => c.namespace },
    { id: 'name', header: 'Claim', sort: (c) => c.name, cell: (c) => <span class="flex items-center gap-2"><span class="font-medium">{c.name}</span><AlertPill e={openAlert(name, 'PersistentVolumeClaim', c.namespace, c.name)} /></span> },
    { id: 'phase', header: 'Phase', cell: (c) => <Pill tone={c.phase === 'Bound' ? 'good' : 'warn'}>{c.phase}</Pill> },
    { id: 'req', header: 'Requested', align: 'right', cell: (c) => fmt.bytes(c.requestedBytes) },
    { id: 'cap', header: 'Capacity', align: 'right', cell: (c) => c.capacityBytes ? fmt.bytes(c.capacityBytes) : '—' },
    { id: 'class', header: 'Class', cell: (c) => c.class || '—' },
    { id: 'vol', header: 'Volume', mono: true, cell: (c) => c.volume || <span class="text-muted">unbound</span> },
    { id: 'age', header: 'Age', cell: (c) => <span class="num text-muted">{c.age}</span> },
  ]
  return (
    <div class="flex flex-col gap-6">
      <ErrorBox error={error} />
      {view && view.classes.length === 0 && <Notice tone="warn">No StorageClass: claims cannot be provisioned.</Notice>}
      <Section title={`Storage classes (${view?.classes.length ?? 0})`}>
        <DataTable loading={!view && !error} search={false} columns={scols} rows={view?.classes ?? []} rowKey={(c) => c.name} empty="None." />
      </Section>
      <Section title={`Persistent volume claims (${view?.claims.length ?? 0})`}>
        <DataTable loading={!view && !error} id="pvcs" columns={ccols} rows={view?.claims ?? []} rowKey={(c) => c.namespace + '/' + c.name} empty="No claims." />
      </Section>
      <Section title={`Persistent volumes (${view?.volumes.length ?? 0})`}>
        <DataTable loading={!view && !error} id="pvs" columns={vcols} rows={view?.volumes ?? []} rowKey={(v) => v.name} empty="No volumes." />
      </Section>
    </div>
  )
}
