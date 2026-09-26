import { useEffect, useMemo, useState } from 'preact/hooks'
import { api, fmt, type Snapshot } from '../../api'
import { formOf } from '../../cluster'
import { DataTable, type Column } from '../../components/DataTable'
import { ConfirmDialog, ErrorBox, Field, Notice, Pill, Section, Tile } from '../../components/ui'
import { loadSnapshots, refreshKey, runningFor, snapshots, toast, watch } from '../../store'
import type { ClusterCtx } from './ClusterPage'

export function Backups({ ctx }: { ctx: ClusterCtx }) {
  const { name, cluster, status } = ctx
  const rows = snapshots.value.get(name) ?? []
  const loaded = snapshots.value.has(name)
  const etcd = cluster.spec.spec.backup?.etcd
  const declared = { interval: etcd?.interval ?? '6h', keep: String(etcd?.keep ?? 28) }
  const [error, setError] = useState<string | null>(null)
  const [restore, setRestore] = useState<Snapshot | null>(null)
  const [remove, setRemove] = useState<Snapshot | null>(null)
  const [schedule, setSchedule] = useState(declared)
  useEffect(() => { loadSnapshots(name) }, [name, refreshKey('*', 'resync')])
  useEffect(() => { setSchedule(declared) }, [cluster.updatedAt])
  const running = runningFor(name).length > 0
  const latest = rows.find((r) => r.status === 'ok')
  const scheduleDirty = schedule.interval !== declared.interval || Number(schedule.keep) !== Number(declared.keep)
  const saveSchedule = () => {
    api.saveClusterForm(name, { ...formOf(cluster.spec.spec), etcdSnapshotInterval: schedule.interval, etcdSnapshotKeep: Number(schedule.keep) || 0 })
      .then(() => { toast('Schedule saved', 'good') }).catch((e) => setError(e.message))
  }

  const columns = useMemo<Column<Snapshot>[]>(() => [
    { id: 'ts', header: 'Taken', sort: (s) => s.ts, cell: (s) => fmt.datetime(s.ts) },
    { id: 'source', header: 'Source', sort: (s) => s.source, cell: (s) => <Pill tone={s.source === 'schedule' ? 'muted' : 'info'}>{s.source}</Pill> },
    { id: 'node', header: 'From', sort: (s) => s.node, mono: true, cell: (s) => s.node },
    { id: 'keys', header: 'Keys', align: 'right', sort: (s) => s.keys, cell: (s) => fmt.int(s.keys) },
    { id: 'size', header: 'Size', align: 'right', sort: (s) => s.sizeBytes, cell: (s) => fmt.bytes(s.sizeBytes) },
    { id: 'versions', header: 'Versions', mono: true, cell: (s) => <span class="text-muted text-[11px]">{s.talosVersion} · {s.k8sVersion}</span> },
    { id: 'status', header: 'Status', sort: (s) => s.status, cell: (s) => <Pill tone={s.status === 'ok' ? 'good' : 'bad'}>{s.status}</Pill> },
    { id: 'offsite', header: 'Off-site', sort: (s) => s.offsite ? 1 : 0, cell: (s) => s.offsite ? <Pill tone="good" title={s.offsite}>copied</Pill> : <span class="text-muted" title="No off-site copy">—</span> },
    { id: 'actions', header: '', align: 'right', cell: (s) => (
      <span class="whitespace-nowrap flex gap-1 justify-end">
        <button class="btn !py-1" title="Unseal, check the hash and open the database" onClick={() => api.verifySnapshot(name, s.id).then((r) => { toast(r.ok ? `Snapshot #${s.id} verified` : `Snapshot #${s.id}: ${r.error}`, r.ok ? 'good' : 'error') }).catch((e) => toast(e.message, 'error'))}>Verify</button>
        <a class="btn !py-1" href={`/api/v1/clusters/${name}/snapshots/${s.id}`} download title="Plain etcd snapshot (.db)">Download</a>
        <button class="btn btn-danger !py-1" disabled={s.status !== 'ok' || running} onClick={() => setRestore(s)}>Restore</button>
        <button class="btn !py-1" onClick={() => setRemove(s)} aria-label="Delete snapshot">✕</button>
      </span>
    ) },
  ], [name, running])
  const cps = cluster.spec.spec.nodes.filter((n) => n.role === 'controlplane')
  return (
    <div class="flex flex-col gap-5">
      <Section title="etcd snapshots" help="Verified, sealed snapshots of the cluster state; manual ones are never pruned."
        actions={<button class="btn btn-primary" disabled={running || !status?.etcd.healthy} title={!status?.etcd.healthy ? 'etcd must be healthy' : ''} onClick={() => api.takeSnapshot(name).then((r) => watch(r)).catch((e) => toast(e.message, 'error'))}>Take snapshot</button>}>
        <ErrorBox error={error} />
        <div class="grid grid-cols-1 md:grid-cols-3 gap-3">
          <Tile size="xl" label="Latest snapshot" value={latest ? fmt.when(latest.ts) : 'none'} sub={latest ? `${fmt.int(latest.keys)} keys · ${fmt.bytes(latest.sizeBytes)}` : 'Take one now or wait for the schedule'} tone={latest ? undefined : 'warn'} />
          <Tile size="xl" label="Schedule" value={schedule.interval === '0' ? 'off' : `every ${schedule.interval}`} sub={`keep ${schedule.keep} scheduled snapshots`} />
          <Tile size="xl" label="Stored" value={String(rows.length)} sub={`${fmt.bytes(rows.reduce((a, r) => a + r.sizeBytes, 0))} uncompressed`} />
        </div>
        <DataTable loading={!loaded} id="snapshots" columns={columns} rows={rows} rowKey={(s) => String(s.id)} defaultSort={{ id: 'ts', dir: 'desc' }} empty="No snapshots yet." />
      </Section>

      <Section title="Schedule" help="Taken when the cluster is healthy and idle.">
        <div class="panel p-3 flex flex-wrap items-end gap-4">
          <Field label="Interval" hint="Go duration ≥ 5m, 0 disables"><input class="input mono w-32" value={schedule.interval} onInput={(e) => setSchedule({ ...schedule, interval: (e.target as HTMLInputElement).value.trim() })} /></Field>
          <Field label="Keep" hint="Scheduled snapshots retained"><input class="input mono w-24" type="number" min={1} value={schedule.keep} onInput={(e) => setSchedule({ ...schedule, keep: (e.target as HTMLInputElement).value })} /></Field>
          <button class="btn btn-primary" disabled={!scheduleDirty} onClick={saveSchedule}>Save</button>
        </div>
      </Section>

      <Section title="Disaster recovery" help="Restore rebuilds the cluster from a snapshot when quorum is lost for good.">
        <Notice tone="warn">Restore wipes etcd on all {cps.length} control plane{cps.length === 1 ? '' : 's'} and rebuilds it from the snapshot on {cps[0]?.hostname}; changes after the snapshot are lost.</Notice>
      </Section>

      {restore && (
        <ConfirmDialog title={`Restore ${name} from snapshot #${restore.id}`} action="Wipe etcd and restore" tone="danger" typed={name} cluster={name} onClose={() => setRestore(null)}
          onConfirm={() => api.restoreSnapshot(name, restore.id).then((r) => { setRestore(null); watch(r) }).catch((e) => toast(e.message, 'error'))}
          impact={<ul class="list-disc pl-5 flex flex-col gap-1">
            <li>Cluster state goes back to <b>{fmt.datetime(restore.ts)}</b> ({fmt.int(restore.keys)} keys, from {restore.node}).</li>
            <li class="text-bad">Every object created or changed since then is lost.</li>
            <li>All {cps.length} control plane{cps.length === 1 ? '' : 's'} reboot with etcd wiped; workers keep running.</li>
            <li>Snapshot taken on {restore.talosVersion} / {restore.k8sVersion}; the cluster runs {cluster.spec.spec.talosVersion} / {cluster.spec.spec.kubernetesVersion}.</li>
          </ul>} />
      )}
      {remove && (
        <ConfirmDialog title={`Delete snapshot #${remove.id}`} action="Delete" tone="danger" onClose={() => setRemove(null)}
          onConfirm={() => api.deleteSnapshot(name, remove.id).then(() => { setRemove(null) }).catch((e) => toast(e.message, 'error'))}
          impact={<p>Deletes the sealed file and its record.</p>} />
      )}
    </div>
  )
}
