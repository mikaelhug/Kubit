import { useMemo } from 'preact/hooks'
import { addonCatalog } from '../../addons'
import { api, fmt, type StorageView } from '../../api'
import { AddonToggle } from '../../components/AddonToggle'
import { DataTable, withoutColumn, type Column } from '../../components/DataTable'
import { NamespaceScope, useNamespaceScope } from '../../components/NamespaceScope'
import { Age } from '../../components/Time'
import { ErrorBox, Notice, Pill, Section } from '../../components/ui'
import { createdSort } from '../../time'
import { phaseTone } from '../../tone'
import { useLive } from '../../useLive'
import type { ClusterCtx } from './ClusterPage'

const longhorn = addonCatalog.find((a) => a.key === 'longhorn')

type SC = StorageView['classes'][number]
type PV = StorageView['volumes'][number]
type PVC = StorageView['claims'][number]

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
  { id: 'phase', header: 'Phase', cell: (v) => <Pill tone={phaseTone(v.phase)}>{v.phase}</Pill> },
  { id: 'class', header: 'Class', cell: (v) => v.class || '—' },
  { id: 'claim', header: 'Claim', mono: true, cell: (v) => v.claim || <span class="text-muted">—</span> },
  { id: 'modes', header: 'Access', cell: (v) => v.accessModes },
  { id: 'reclaim', header: 'Reclaim', cell: (v) => v.reclaim },
  { id: 'age', header: 'Age', sort: createdSort, cell: (v) => <span class="text-muted"><Age at={v.createdAt} fallback={v.age} /></span> },
]

export function Storage({ ctx }: { ctx: ClusterCtx }) {
  const { name, cluster } = ctx
  const platform = cluster.spec.spec.platform
  const { data: view, error } = useLive(() => api.storage(name), [name], [[name, 'storage']])
  const s = useNamespaceScope(name, view ? view.claims.map((c) => c.namespace) : null)
  const claims = useMemo(() => (view?.claims ?? []).filter((c) => s.keep(c.namespace)), [view, s.keep])
  const ccols = useMemo(() => withoutColumn<PVC>([
    { id: 'ns', header: 'Namespace', sort: (c) => c.namespace, cell: (c) => c.namespace },
    { id: 'name', header: 'Claim', sort: (c) => c.name, cell: (c) => <span class="font-medium">{c.name}</span> },
    { id: 'phase', header: 'Phase', cell: (c) => <Pill tone={phaseTone(c.phase)}>{c.phase}</Pill> },
    { id: 'req', header: 'Requested', align: 'right', cell: (c) => fmt.bytes(c.requestedBytes) },
    { id: 'cap', header: 'Capacity', align: 'right', cell: (c) => c.capacityBytes ? fmt.bytes(c.capacityBytes) : '—' },
    { id: 'class', header: 'Class', cell: (c) => c.class || '—' },
    { id: 'vol', header: 'Volume', mono: true, cell: (c) => c.volume || <span class="text-muted">unbound</span> },
    { id: 'age', header: 'Age', sort: createdSort, cell: (c) => <span class="text-muted"><Age at={c.createdAt} fallback={c.age} /></span> },
  ], 'ns', !!s.ns), [s.ns])
  const loading = !view && !error
  return (
    <div class="flex flex-col gap-5">
      <ErrorBox error={error} />
      {view && view.classes.length === 0 && (
        <Notice tone="warn">
          <span class="flex flex-wrap items-center gap-2">
            No StorageClass.
            <span class="ml-auto">{longhorn && !platform.longhorn.enabled ? <AddonToggle cluster={name} info={longhorn} platform={platform} label="Enable Longhorn" /> : <a href={`/clusters/${name}/changes`} class="text-accent hover:underline text-[12px]">Review changes</a>}</span>
          </span>
        </Notice>
      )}
      {!(view && view.classes.length === 0) && (
        <Section title={`Storage classes (${view?.classes.length ?? 0})`}>
          <DataTable loading={loading} search={false} columns={scols} rows={view?.classes ?? []} rowKey={(c) => c.name} empty="None." />
        </Section>
      )}
      <Section title={`Persistent volume claims (${claims.length})`} actions={<NamespaceScope s={s} rows={(view?.claims ?? []).map((c) => c.namespace)} />}>
        <DataTable loading={loading || s.loading} id="pvcs" columns={ccols} rows={claims} rowKey={(c) => c.namespace + '/' + c.name} empty={s.scope === 'apps' && !s.ns ? 'No app claims yet.' : 'No claims.'} />
      </Section>
      <Section title={`Persistent volumes (${view?.volumes.length ?? 0})`}>
        <DataTable loading={loading} id="pvs" columns={vcols} rows={view?.volumes ?? []} rowKey={(v) => v.name} empty="No volumes." />
      </Section>
    </div>
  )
}
