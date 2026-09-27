import { useMemo, useState } from 'preact/hooks'
import { api, fmt, type LabVM, type NodeRow } from '../../api'
import { hostName, hostOf, labOffline, onMac, vmsOf } from '../../machine'
import { runOp } from '../../ops'
import { machines, toast } from '../../store'
import { DataTable, type Column } from '../DataTable'
import { KindPill } from '../Machine'
import { ConfirmDialog, Notice, Pill } from '../ui'
import { AddVMsDialog, ResizeVMDialog } from './dialogs'
import { mib, reserveOf, totalMem } from './plan'

const vmOp = (p: Promise<{ operationId: number }>) => runOp(p, undefined, false)

export function HostVMs({ host }: { host: NodeRow }) {
  const lh = host.labhost
  const [add, setAdd] = useState(false)
  const [del, setDel] = useState<LabVM | null>(null)
  const [resize, setResize] = useState<LabVM | null>(null)
  const byMac = machines.value
  const offline = labOffline(lh)
  const columns = useMemo<Column<LabVM>[]>(() => [
    { id: 'vm', header: 'VM', cell: (vm) => { const row = byMac.get(vm.mac); return (
      <span class="flex flex-col min-w-0">
        {row ? <a class="font-medium mono hover:underline" href={`/machines/${row.mac}`}>{vm.name}</a> : <span class="font-medium mono">{vm.name}</span>}
        <span class="text-[10px] text-muted mono">{vm.mac}{row?.ip || vm.ip ? ` · ${row?.ip || vm.ip}` : ''}</span>
      </span>
    ) } },
    { id: 'state', header: 'State', cell: (vm) => <Pill tone={!offline && vm.state === 'running' ? 'good' : 'muted'}>{vm.state}</Pill> },
    { id: 'size', header: 'Size', cell: (vm) => <span>{vm.cpus} vCPU · {fmt.bytes(mib(vm.memMiB))} · {vm.diskGiB} GiB{vm.dataGiB ? ` + ${vm.dataGiB} GiB data` : ''}</span> },
    { id: 'boot', header: 'Boot', cell: (vm) => <Pill tone={vm.boot === 'disk' ? 'info' : 'muted'}>{vm.boot === 'disk' ? 'disk' : onMac(lh) ? 'Talos ISO' : 'Talos (RAM)'}</Pill> },
    { id: 'kubit', header: 'Kubit', cell: (vm) => { const row = byMac.get(vm.mac); return row ? (row.cluster ? <a class="text-accent hover:underline" href={`/clusters/${row.cluster}/nodes`}>{row.cluster} · {row.hostname}</a> : <KindPill m={row} />) : <span class="text-muted">—</span> } },
    { id: 'actions', header: '', align: 'right', cell: (vm) => { const member = !!byMac.get(vm.mac)?.cluster; return (
      <span class="flex gap-1 justify-end">
        {vm.state === 'running' ? <button class="btn btn-sm" disabled={member || offline} title={member ? 'Drain and remove it from the cluster first' : ''} onClick={() => vmOp(api.labVM(host.mac, vm.name, 'stop'))}>Stop</button> : <button class="btn btn-sm" disabled={offline} onClick={() => vmOp(api.labVM(host.mac, vm.name, 'start'))}>Start</button>}
        <button class="btn btn-sm" disabled={offline} onClick={() => setResize(vm)}>Resize</button>
        <button class="btn btn-sm" disabled={member || offline} title={member ? 'Remove it from the cluster first' : 'Boot back into Talos maintenance mode'} onClick={() => vmOp(api.labVM(host.mac, vm.name, 'reprovision'))}>Re-provision</button>
        <button class="btn btn-danger btn-sm" disabled={member || offline} onClick={() => setDel(vm)}>Delete</button>
      </span>
    ) } },
  ], [byMac, offline, lh, host.mac])
  if (!lh) return null
  const vms = vmsOf(lh)
  return (
    <div class="flex flex-col gap-4">
      <DataTable search={false} columns={columns} rows={vms} rowKey={(vm) => vm.name} empty="No VMs yet."
        title={<><span class="font-medium">Virtual machines</span><span class="text-[12px] text-muted">{fmt.bytes(mib(totalMem(vms)))} of {fmt.bytes(mib(Math.max(0, lh.capacity.memMiB - reserveOf(lh))))} assigned</span></>}
        toolbar={<button class="btn btn-primary btn-sm" disabled={lh.state !== 'ready' || offline} onClick={() => setAdd(true)}>+ Add VMs</button>} />
      {add && <AddVMsDialog host={host} onClose={() => setAdd(false)} />}
      {resize && <ResizeVMDialog host={host} vm={resize} onClose={() => setResize(null)} />}
      {del && <ConfirmDialog title={`Delete ${del.name}`} action="Delete VM" tone="danger" onClose={() => setDel(null)} onConfirm={() => api.labVMDelete(host.mac, del.name).then(() => setDel(null)).catch((e) => toast(e.message, 'error'))} impact={<p>Destroys the VM and its disk.</p>} />}
    </div>
  )
}

export function LabVMControls({ vm }: { vm: NodeRow }) {
  const host = hostOf(vm)
  const entry = vmsOf(host?.labhost).find((v) => v.mac === vm.mac)
  const [del, setDel] = useState(false)
  if (!host || !entry) return <Notice tone="muted">Its lab host is no longer known.</Notice>
  const member = !!vm.cluster
  return (
    <div class="panel p-3 flex items-center gap-4">
      <div class="flex-1 min-w-0">
        <div class="font-medium">VM on <a class="text-accent hover:underline" href={`/labhosts/${host.mac}/overview`}>{hostName(host)}</a></div>
        <p class="text-[12.5px] text-muted">{entry.cpus} vCPU · {fmt.bytes(mib(entry.memMiB))} · {entry.diskGiB} GiB · {entry.state}{entry.boot === 'disk' ? ' · boots from disk' : onMac(host.labhost) ? ' · boots the Talos ISO' : ' · boots Talos over the network'}</p>
      </div>
      <div class="flex gap-2 shrink-0">
        {entry.state === 'running' ? <button class="btn" disabled={member} title={member ? 'Drain and remove it from the cluster first' : ''} onClick={() => vmOp(api.labVM(host.mac, entry.name, 'stop'))}>Stop</button> : <button class="btn btn-primary" onClick={() => vmOp(api.labVM(host.mac, entry.name, 'start'))}>Start</button>}
        <button class="btn" disabled={member} title={member ? 'Remove it from the cluster first' : 'Boot back into Talos maintenance mode'} onClick={() => vmOp(api.labVM(host.mac, entry.name, 'reprovision'))}>Re-provision</button>
        <button class="btn btn-danger" disabled={member} onClick={() => setDel(true)}>Delete</button>
      </div>
      {del && <ConfirmDialog title={`Delete ${entry.name}`} action="Delete VM" tone="danger" onClose={() => setDel(false)} onConfirm={() => api.labVMDelete(host.mac, entry.name).then(() => setDel(false)).catch((e) => toast(e.message, 'error'))} impact={<p>Destroys the VM and its disk.</p>} />}
    </div>
  )
}
