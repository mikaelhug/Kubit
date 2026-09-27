import { useEffect, useMemo, useState } from 'preact/hooks'
import { api, authedUrl, fmt, type Snapshot } from '../../api'
import { formOf } from '../../cluster'
import { DataTable, type Column } from '../../components/DataTable'
import { ConfirmDialog, ErrorBox, Field, MovedNotice, Notice, Pill, Section, Tile } from '../../components/ui'
import { runningFor, runOp } from '../../ops'
import { loadSnapshots, snapshots, toast } from '../../store'
import { useDraft } from '../../useDraft'
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
  const d = useDraft(declared, `cluster:${name}:schedule`)
  const schedule = d.draft ?? declared
  useEffect(() => { loadSnapshots(name) }, [name])
  const running = runningFor(name).length > 0
  const latest = rows.find((r) => r.status === 'ok')
  const scheduleDirty = d.dirty && (schedule.interval !== declared.interval || Number(schedule.keep) !== Number(declared.keep))
  const saveSchedule = () => {
    api.saveClusterForm(name, { ...formOf(cluster.spec.spec), etcdSnapshotInterval: schedule.interval, etcdSnapshotKeep: Number(schedule.keep) || 0 })
      .then(() => { d.commit(); toast('Schedule saved', 'good') }).catch((e) => setError(e.message))
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
        <button class="btn btn-sm" title="Unseal, check the hash and open the database" onClick={() => api.verifySnapshot(name, s.id).then((r) => { toast(r.ok ? `Snapshot #${s.id} verified` : `Snapshot #${s.id}: ${r.error}`, r.ok ? 'good' : 'error') }).catch((e) => toast(e.message, 'error'))}>Verify</button>
        <a class="btn btn-sm" href={authedUrl(`/clusters/${name}/snapshots/${s.id}`)} download title="Plain etcd snapshot (.db)">Download</a>
        <button class="btn btn-danger btn-sm" disabled={s.status !== 'ok' || running} onClick={() => setRestore(s)}>Restore</button>
        <button class="btn btn-sm" onClick={() => setRemove(s)} aria-label="Delete snapshot">✕</button>
      </span>
    ) },
  ], [name, running])
  const cps = cluster.spec.spec.nodes.filter((n) => n.role === 'controlplane')
  return (
    <div class="flex flex-col gap-5">
      <Section title="etcd snapshots" help="Verified, sealed snapshots of the cluster state; manual ones are never pruned."
        actions={<button class="btn btn-primary" disabled={running || !status?.etcd.healthy} title={!status?.etcd.healthy ? 'etcd must be healthy' : ''} onClick={() => runOp(api.takeSnapshot(name))}>Take snapshot</button>}>
        <ErrorBox error={error} />
        <div class="grid grid-cols-1 md:grid-cols-3 gap-3">
          <Tile size="xl" label="Latest snapshot" value={latest ? fmt.when(latest.ts) : 'none'} sub={latest ? `${fmt.int(latest.keys)} keys · ${fmt.bytes(latest.sizeBytes)}` : 'Take one now or wait for the schedule'} tone={latest ? undefined : 'warn'} />
          <Tile size="xl" label="Schedule" value={schedule.interval === '0' ? 'off' : `every ${schedule.interval}`} sub={`keep ${schedule.keep} scheduled snapshots`} />
          <Tile size="xl" label="Stored" value={String(rows.length)} sub={`${fmt.bytes(rows.reduce((a, r) => a + r.sizeBytes, 0))} uncompressed`} />
        </div>
        <DataTable loading={!loaded} id="snapshots" columns={columns} rows={rows} rowKey={(s) => String(s.id)} defaultSort={{ id: 'ts', dir: 'desc' }} empty="No snapshots yet." />
      </Section>

      <Section title="Schedule" help="Taken when the cluster is healthy and idle.">
        <MovedNotice show={d.moved} onDiscard={d.discard} />
        <div class="panel p-3 flex flex-wrap items-end gap-4">
          <Field label="Interval" hint="Go duration ≥ 5m, 0 disables"><input class="input mono w-32" value={schedule.interval} onInput={(e) => d.set({ ...schedule, interval: (e.target as HTMLInputElement).value.trim() })} /></Field>
          <Field label="Keep" hint="Scheduled snapshots retained"><input class="input mono w-24" type="number" min={1} value={schedule.keep} onInput={(e) => d.set({ ...schedule, keep: (e.target as HTMLInputElement).value })} /></Field>
          <button class="btn btn-primary" disabled={!scheduleDirty} onClick={saveSchedule}>Save</button>
        </div>
      </Section>

      <Section title="Disaster recovery" help="Restore rebuilds the cluster from a snapshot when quorum is lost for good.">
        <Notice tone="warn">Restore wipes etcd on all {cps.length} control plane{cps.length === 1 ? '' : 's'} and rebuilds it from the snapshot on {cps[0]?.hostname}; changes after the snapshot are lost.</Notice>
      </Section>

      {restore && (
        <ConfirmDialog title={`Restore ${name} from snapshot #${restore.id}`} action="Wipe etcd and restore" tone="danger" typed={name} cluster={name} onClose={() => setRestore(null)}
          onConfirm={() => runOp(api.restoreSnapshot(name, restore.id)).then((ok) => { if (ok) setRestore(null) })}
          impact={<>
            <p>State returns to <b>{fmt.datetime(restore.ts)}</b>; later changes are lost and the control planes reboot.</p>
            {(restore.talosVersion !== cluster.spec.spec.talosVersion || restore.k8sVersion !== cluster.spec.spec.kubernetesVersion) && <p class="text-warn">Taken on {restore.talosVersion} / {restore.k8sVersion}; the cluster runs {cluster.spec.spec.talosVersion} / {cluster.spec.spec.kubernetesVersion}.</p>}
          </>} />
      )}
      {remove && (
        <ConfirmDialog title={`Delete snapshot #${remove.id}`} action="Delete" tone="danger" onClose={() => setRemove(null)}
          onConfirm={() => api.deleteSnapshot(name, remove.id).then(() => { setRemove(null) }).catch((e) => toast(e.message, 'error'))}
          impact={<p>Deletes the sealed file and its record.</p>} />
      )}
    </div>
  )
}
