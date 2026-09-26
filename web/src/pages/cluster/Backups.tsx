import { useEffect, useState } from 'preact/hooks'
import { api, fmt, formOf, type Snapshot } from '../../api'
import { loadSnapshots, operations, refreshKey, snapshots, toast, watch } from '../../store'
import { DataTable, type Column } from '../../components/DataTable'
import { ConfirmDialog, ErrorBox, Field, Notice, Pill, Section } from '../../components/ui'
import type { ClusterCtx } from './ClusterPage'

/**
 * etcd snapshots kept sealed on the admin host. Restore is the disaster-recovery path:
 * it wipes etcd on every control plane and rebuilds the cluster state from a snapshot.
 */
export function Backups({ ctx }: { ctx: ClusterCtx }) {
  const { name, cluster, status } = ctx
  const rows = snapshots.value.get(name) ?? []
  const loaded = snapshots.value.has(name)
  const [error, setError] = useState<string | null>(null)
  const [restore, setRestore] = useState<Snapshot | null>(null)
  const [remove, setRemove] = useState<Snapshot | null>(null)
  const [schedule, setSchedule] = useState({ interval: cluster.spec.spec.backup?.etcd.interval ?? '6h', keep: String(cluster.spec.spec.backup?.etcd.keep ?? 28) })
  useEffect(() => { loadSnapshots(name) }, [name, refreshKey('*', 'resync')])
  useEffect(() => { setSchedule({ interval: cluster.spec.spec.backup?.etcd.interval ?? '6h', keep: String(cluster.spec.spec.backup?.etcd.keep ?? 28) }) }, [cluster.updatedAt])
  const running = [...operations.value.values()].some((o) => o.cluster === name && o.status === 'running')
  const latest = rows.find((r) => r.status === 'ok')
  const scheduleDirty = schedule.interval !== (cluster.spec.spec.backup?.etcd.interval ?? '6h') || Number(schedule.keep) !== (cluster.spec.spec.backup?.etcd.keep ?? 28)
  const saveSchedule = () => {
    api.saveClusterForm(name, { ...formOf(cluster.spec.spec), etcdSnapshotInterval: schedule.interval, etcdSnapshotKeep: Number(schedule.keep) || 0 })
      .then(() => { toast('Schedule saved', 'good') }).catch((e) => setError(e.message))
  }

  const columns: Column<Snapshot>[] = [
    { id: 'ts', header: 'Taken', sort: (s) => s.ts, cell: (s) => <span class="num">{fmt.datetime(s.ts)}</span> },
    { id: 'source', header: 'Source', sort: (s) => s.source, cell: (s) => <Pill tone={s.source === 'schedule' ? 'muted' : 'info'}>{s.source}</Pill> },
    { id: 'node', header: 'From', sort: (s) => s.node, mono: true, cell: (s) => s.node },
    { id: 'keys', header: 'Keys', align: 'right', sort: (s) => s.keys, cell: (s) => s.keys.toLocaleString() },
    { id: 'size', header: 'Size', align: 'right', sort: (s) => s.sizeBytes, cell: (s) => fmt.bytes(s.sizeBytes) },
    { id: 'versions', header: 'Versions', mono: true, cell: (s) => <span class="text-muted text-[11px]">{s.talosVersion} · {s.k8sVersion}</span> },
    { id: 'status', header: 'Status', sort: (s) => s.status, cell: (s) => <Pill tone={s.status === 'ok' ? 'good' : 'bad'}>{s.status}</Pill> },
    { id: 'offsite', header: 'Off-site', sort: (s) => s.offsite ? 1 : 0, cell: (s) => s.offsite ? <Pill tone="good" title={s.offsite}>copied</Pill> : <span class="text-muted" title="No off-site copy: target off or the copy failed">—</span> },
    { id: 'actions', header: '', align: 'right', cell: (s) => (
      <span class="whitespace-nowrap flex gap-1 justify-end">
        <button class="btn !py-1" title="Unseal, check the hash and open the database" onClick={() => api.verifySnapshot(name, s.id).then((r) => { toast(r.ok ? `Snapshot #${s.id} verified` : `Snapshot #${s.id}: ${r.error}`, r.ok ? 'good' : 'error') }).catch((e) => toast(e.message, 'error'))}>Verify</button>
        <a class="btn !py-1" href={`/api/v1/clusters/${name}/snapshots/${s.id}`} download title="Plain etcd snapshot (.db) for talosctl or etcdutl">Download</a>
        <button class="btn btn-danger !py-1" disabled={s.status !== 'ok' || running} onClick={() => setRestore(s)}>Restore</button>
        <button class="btn !py-1" onClick={() => setRemove(s)} aria-label="Delete snapshot">✕</button>
      </span>
    ) },
  ]
  const cps = cluster.spec.spec.nodes.filter((n) => n.role === 'controlplane')
  return (
    <div class="flex flex-col gap-5">
      <Section title="etcd snapshots" help="Consistent, verified, sealed snapshots of the cluster state. Scheduled ones are pruned to the retention count; manual ones stay."
        actions={<button class="btn btn-primary" disabled={running || !status?.etcd.healthy} title={!status?.etcd.healthy ? 'etcd must be healthy' : ''} onClick={() => api.takeSnapshot(name).then((r) => watch(r)).catch((e) => toast(e.message, 'error'))}>Take snapshot</button>}>
        <ErrorBox error={error} />
        <div class="grid grid-cols-1 md:grid-cols-3 gap-3">
          <Stat label="Latest snapshot" value={latest ? fmt.when(latest.ts) : 'none'} sub={latest ? `${latest.keys.toLocaleString()} keys · ${fmt.bytes(latest.sizeBytes)}` : 'Take one now or wait for the schedule'} tone={latest ? undefined : 'warn'} />
          <Stat label="Schedule" value={schedule.interval === '0' ? 'off' : `every ${schedule.interval}`} sub={`keep ${schedule.keep} scheduled snapshots`} />
          <Stat label="Stored" value={String(rows.length)} sub={`${fmt.bytes(rows.reduce((a, r) => a + r.sizeBytes, 0))} uncompressed`} />
        </div>
        <DataTable loading={!loaded} id="snapshots" columns={columns} rows={rows} rowKey={(s) => String(s.id)} defaultSort={{ id: 'ts', dir: 'desc' }} empty="No snapshots yet." />
      </Section>

      <Section title="Schedule" help="Taken when the cluster is healthy and idle.">
        <div class="panel p-3 flex flex-wrap items-end gap-4">
          <Field label="Interval" hint="Go duration ≥ 5m, or 0 to disable."><input class="input mono w-32" value={schedule.interval} onInput={(e) => setSchedule({ ...schedule, interval: (e.target as HTMLInputElement).value.trim() })} /></Field>
          <Field label="Keep" hint="Scheduled snapshots retained."><input class="input mono w-24" type="number" min={1} value={schedule.keep} onInput={(e) => setSchedule({ ...schedule, keep: (e.target as HTMLInputElement).value })} /></Field>
          <button class="btn btn-primary" disabled={!scheduleDirty} onClick={saveSchedule}>Save</button>
        </div>
      </Section>

      <Section title="Disaster recovery" help="Restore rebuilds the cluster from a snapshot when quorum is lost for good. Changes after the snapshot are lost.">
        <Notice tone="warn">Restore wipes the EPHEMERAL partition on all {cps.length} control plane{cps.length === 1 ? '' : 's'} (machine configs on STATE are kept), uploads the snapshot to {cps[0]?.hostname}, bootstraps etcd from it and waits for the other members to rejoin and every node to become Ready. Verified on a 3-control-plane cluster in ~3 minutes. Take a fresh snapshot first if the cluster is still healthy.</Notice>
      </Section>

      {restore && (
        <ConfirmDialog title={`Restore ${name} from snapshot #${restore.id}`} action="Wipe etcd and restore" tone="danger" typed={name} cluster={name} onClose={() => setRestore(null)}
          onConfirm={() => api.restoreSnapshot(name, restore.id).then((r) => { setRestore(null); watch(r) }).catch((e) => toast(e.message, 'error'))}
          impact={<ul class="list-disc pl-5 flex flex-col gap-1">
            <li>Cluster state goes back to <b>{fmt.datetime(restore.ts)}</b> ({restore.keys.toLocaleString()} keys, taken from {restore.node}).</li>
            <li class="text-bad">Every object created or changed since then is lost: deployments, secrets, PVCs, MetalLB assignments, Flux state.</li>
            <li>All {cps.length} control plane{cps.length === 1 ? '' : 's'} reboot with etcd wiped; the API is down for ~1–2 minutes.</li>
            <li>Workers are not touched; pods keep running and reconcile against the restored state.</li>
            <li>Talos and Kubernetes versions are unchanged: the snapshot was taken on {restore.talosVersion} / {restore.k8sVersion}; the cluster runs {cluster.spec.spec.talosVersion} / {cluster.spec.spec.kubernetesVersion}.</li>
          </ul>} />
      )}
      {remove && (
        <ConfirmDialog title={`Delete snapshot #${remove.id}`} action="Delete" tone="danger" onClose={() => setRemove(null)}
          onConfirm={() => api.deleteSnapshot(name, remove.id).then(() => { setRemove(null) }).catch((e) => toast(e.message, 'error'))}
          impact={<p>Deletes the sealed file and its record. {remove.source === 'manual' ? 'Manual snapshots are never pruned automatically.' : ''}</p>} />
      )}
    </div>
  )
}

function Stat({ label, value, sub, tone }: { label: string; value: string; sub?: string; tone?: 'warn' }) {
  return (
    <div class="panel p-3 flex flex-col gap-1">
      <span class="label">{label}</span>
      <span class={`text-xl font-semibold num ${tone === 'warn' ? 'text-warn' : ''}`}>{value}</span>
      {sub && <span class="text-[12px] text-muted">{sub}</span>}
    </div>
  )
}
