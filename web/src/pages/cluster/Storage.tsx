import { useMemo, useState } from 'preact/hooks'
import { addonCatalog } from '../../addons'
import { api, fmt, type LonghornView, type NodeStatus, type Status, type StorageNode, type StorageView } from '../../api'
import { AddonToggle } from '../../components/AddonToggle'
import { DataTable, withoutColumn, type Column } from '../../components/DataTable'
import { NamespaceScope, useNamespaceScope } from '../../components/NamespaceScope'
import { ReplicasSelect } from '../../components/ReplicasSelect'
import { Age } from '../../components/Time'
import { Check, ErrorBox, Notice, Pending, Pill, Section, UsedOf } from '../../components/ui'
import { longhornCapacity } from '../../components/LonghornUsage'
import { kubeHealth, roleLabel, talosHealth } from '../../machine'
import { toast, writeClusterYaml } from '../../store'
import { createdSort } from '../../time'
import { phaseTone, type Tone } from '../../tone'
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
  { id: 'replicas', header: 'Replicas', sort: (v) => v.longhorn?.replicas ?? -1, cell: (v) => v.longhorn ? <span class="inline-flex items-center gap-2">{v.longhorn.replicas}<VolumeHealth v={v.longhorn} /></span> : <span class="text-muted">—</span> },
  { id: 'age', header: 'Age', sort: createdSort, cell: (v) => <span class="text-muted"><Age at={v.createdAt} fallback={v.age} /></span> },
]

const robustness: Record<string, Tone> = { healthy: 'good', degraded: 'warn', faulted: 'bad' }

function VolumeHealth({ v }: { v: NonNullable<PV['longhorn']> }) {
  if (v.state === 'detached') return <Pill tone="muted">detached</Pill>
  return <Pill tone={robustness[v.robustness] ?? 'muted'}>{v.robustness || v.state}</Pill>
}


function nodeState(n: StorageNode, status: NodeStatus | undefined, apiReachable: boolean): [Tone, string, string?] {
  if (n.awaiting) return ['info', 'awaiting install']
  if (!n.disks.length) return ['muted', 'no storage']
  if (status && apiReachable) {
    const talos = talosHealth(status)
    if (talos) return [talos[0], talos[1], status.talosReachable ? undefined : status.talosError]
    const kube = kubeHealth(status, true)
    if (kube[0] !== 'good') return kube
  }
  const l = n.live
  if (!l) return ['warn', 'not in Longhorn']
  if (!l.ready) return ['bad', 'down', l.reason]
  if (!l.schedulable) return ['warn', 'not schedulable', l.reason]
  return ['good', 'ready']
}

function LonghornSection({ cluster, macs, view, status }: { cluster: string; macs: Map<string, string>; view: LonghornView; status: Status | null }) {
  const nodeStatus = new Map((status?.nodes ?? []).map((n) => [n.hostname, n]))
  const [busy, setBusy] = useState(false)
  const write = (f: (hash: string) => Promise<{ hash: string }>) => {
    setBusy(true)
    writeClusterYaml(cluster, f).catch((e) => toast(e.message, 'error')).finally(() => setBusy(false))
  }
  const columns: Column<StorageNode>[] = [
    { id: 'node', header: 'Node', sort: (n) => n.hostname, cell: (n) => macs.get(n.hostname) ? <a class="font-medium hover:underline" href={`/machines/${macs.get(n.hostname)}`}>{n.hostname}</a> : <span class="font-medium">{n.hostname}</span> },
    { id: 'role', header: 'Role', cell: (n) => roleLabel(n.role) },
    { id: 'disks', header: 'Disks', cell: (n) => n.editable
      ? <Check checked={n.systemDisk} disabled={busy} onChange={(on) => write((hash) => api.setNodeStorage(cluster, n.hostname, { systemDisk: on, hash }))}>data-system</Check>
      : n.disks.length ? <span class="mono">{n.disks.map((d) => d.split('/').pop()).join(', ')}</span> : <span class="text-muted">—</span> },
    { id: 'scheduled', header: 'Scheduled', align: 'right', sort: (n) => n.live?.scheduledBytes ?? -1, cell: (n) => n.live ? <UsedOf used={n.live.scheduledBytes} cap={longhornCapacity(n.live)} /> : <span class="text-muted">—</span> },
    { id: 'used', header: 'Used', align: 'right', sort: (n) => n.live ? n.live.maximumBytes - n.live.availableBytes : -1, cell: (n) => n.live ? <UsedOf used={n.live.maximumBytes - n.live.availableBytes} cap={n.live.maximumBytes} /> : <span class="text-muted">—</span> },
    { id: 'replicas', header: 'Replicas', align: 'right', sort: (n) => n.live?.replicas ?? -1, cell: (n) => n.live ? n.live.replicas : <span class="text-muted">—</span> },
    { id: 'state', header: 'State', cell: (n) => { const [tone, label, reason] = nodeState(n, nodeStatus.get(n.hostname), !!status?.apiReachable); return <span class="inline-flex items-center gap-2"><Pill tone={tone}>{label}</Pill>{reason && <span class="text-[12px] text-muted">{reason}</span>}</span> } },
  ]
  return (
    <Section title="Longhorn" actions={
      <span class="flex flex-wrap items-center gap-3">
        {view.live && view.applied > 0 && <span class="mono text-[12px] text-muted">{fmt.bytes(view.usableBytes)} usable at {view.applied} {view.applied === 1 ? 'replica' : 'replicas'}</span>}
        {view.live && view.applied > 0 && (view.replicas || view.auto) !== view.applied && <Pending cluster={cluster} />}
        <ReplicasSelect value={view.replicas} auto={view.auto} max={view.max} usable={view.usableByReplicas} disabled={busy} onChange={(replicas) => write((hash) => api.setAddon(cluster, 'longhorn', { enabled: true, replicas, hash }))} />
      </span>
    }>
      {view.error ? <Notice tone="bad">{view.error}</Notice> : !view.live && <Notice tone="warn">Longhorn is not installed.</Notice>}
      <DataTable search={false} id="longhorn-nodes" columns={columns} rows={view.nodes} rowKey={(n) => n.hostname} empty="No node runs workloads." />
    </Section>
  )
}

export function Storage({ ctx }: { ctx: ClusterCtx }) {
  const { name, cluster } = ctx
  const platform = cluster.spec.spec.platform
  const { data: view, error } = useLive(() => api.storage(name), [name], [[name, 'storage'], [name, 'config']])
  const macs = useMemo(() => new Map(cluster.spec.spec.nodes.flatMap((n) => n.mac ? [[n.hostname, n.mac] as const] : [])), [cluster.spec.spec.nodes])
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
      {view?.longhorn && <LonghornSection cluster={name} macs={macs} view={view.longhorn} status={ctx.status} />}
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
