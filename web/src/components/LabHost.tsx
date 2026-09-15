import { useState } from 'preact/hooks'
import { api, fmt, type LabVM, type NodeRow } from '../api'
import { machineList, toast, watch } from '../store'
import { ConfirmDialog, Dialog, ErrorBox, Field, Meter, Notice, Pill } from './ui'

const RESERVED_MIB = 2048

/** Sizes VMs against what the host has left; the suggestion is one control plane plus workers. */
export function AddVMsDialog({ host, onClose }: { host: NodeRow; onClose: () => void }) {
  const lh = host.labhost!
  const used = lh.vms.reduce((s, v) => s + v.memMiB, 0)
  const freeMiB = Math.max(0, lh.capacity.memMiB - RESERVED_MIB - used)
  const [count, setCount] = useState(Math.min(4, Math.max(1, Math.floor(freeMiB / 3072))))
  const [cpus, setCpus] = useState(2)
  const [mem, setMem] = useState(3072)
  const [disk, setDisk] = useState(20)
  const [prefix, setPrefix] = useState('vm')
  const [error, setError] = useState<string | null>(null)
  const need = count * mem
  const over = need > freeMiB
  const overCpu = count * cpus > lh.capacity.cpus * 2
  const submit = () => api.labAddVMs(host.mac, { count, cpus, memMiB: mem, diskGiB: disk, prefix }).then((r) => { onClose(); watch(r) }).catch((e) => setError(e.message))
  return (
    <Dialog title={`Add VMs on ${lh.capacity.hostname || host.hostname}`} onClose={onClose} footer={<><button class="btn" onClick={onClose}>Cancel</button><button class="btn btn-primary" disabled={over || count < 1} onClick={submit}>Create {count} VM{count === 1 ? '' : 's'}</button></>}>
      <ErrorBox error={error} />
      <div class="grid grid-cols-2 gap-3">
        <Field label="How many"><input class="input num" type="number" min={1} max={32} value={count} onInput={(e) => setCount(Number((e.target as HTMLInputElement).value))} /></Field>
        <Field label="Name prefix"><input class="input mono" value={prefix} onInput={(e) => setPrefix((e.target as HTMLInputElement).value.trim())} /></Field>
        <Field label="vCPUs each" hint={`${lh.capacity.cpus} on the host; oversubscribing 2× is fine for a lab.`}><input class="input num" type="number" min={1} max={16} value={cpus} onInput={(e) => setCpus(Number((e.target as HTMLInputElement).value))} /></Field>
        <Field label="RAM each (MiB)" hint="Control planes want 2 GiB+, workers 1 GiB+; 3 GiB is a comfortable lab node."><input class="input num" type="number" min={1024} step={256} value={mem} onInput={(e) => setMem(Number((e.target as HTMLInputElement).value))} /></Field>
        <Field label="Disk each (GiB)" hint="Thin-provisioned; only used space costs."><input class="input num" type="number" min={8} value={disk} onInput={(e) => setDisk(Number((e.target as HTMLInputElement).value))} /></Field>
      </div>
      <Meter label={`Memory: ${fmt.bytes(need << 20)} of ${fmt.bytes(freeMiB << 20)} free (host keeps 2 GiB)`} used={need} cap={Math.max(freeMiB, 1)} format={(n) => fmt.bytes(n << 20)} />
      {over && <Notice tone="bad">Not enough memory: reduce the count or the RAM per VM.</Notice>}
      {overCpu && <Notice tone="warn">More than 2× the host's CPUs; the VMs will contend.</Notice>}
      {count >= 4 && <Notice tone="muted">Suggestion for {count}: 1 control plane + {count - 1} workers (no HA — the host is one failure domain anyway), or 3 control planes + {count - 3} workers to rehearse HA.</Notice>}
      <p class="text-[12px] text-muted">The VMs boot Talos into maintenance mode straight from the host and appear as machines within a minute or two. Nothing is installed on their disks until you create or adopt a cluster.</p>
    </Dialog>
  )
}

/** The Lab host tab on a machine page: capacity, VMs, actions. */
export function LabHostPanel({ host }: { host: NodeRow }) {
  const lh = host.labhost
  const [add, setAdd] = useState(false)
  const [release, setRelease] = useState(false)
  const [del, setDel] = useState<LabVM | null>(null)
  if (!lh) return null
  const rows = machineList.value
  const rowOf = (vm: LabVM) => rows.find((m) => m.mac === vm.mac)
  const used = lh.vms.reduce((s, v) => s + v.memMiB, 0)
  const run = (p: Promise<{ operationId: number }>) => p.then((r) => watch(r, false)).catch((e) => toast(e.message, 'error'))
  return (
    <div class="flex flex-col gap-4">
      {lh.state === 'error' && <Notice tone="bad">Lab host setup failed: {lh.error}</Notice>}
      {lh.state === 'installing' && <Notice tone="warn">Debian is being installed unattended; this takes about ten minutes. Progress is in the Activity drawer.</Notice>}
      <div class="grid grid-cols-1 md:grid-cols-3 gap-4">
        <div class="panel p-4"><Meter label={`Memory for VMs (${lh.vms.length} VM${lh.vms.length === 1 ? '' : 's'})`} used={used} cap={Math.max(1, lh.capacity.memMiB - RESERVED_MIB)} format={(n) => fmt.bytes(n << 20)} /></div>
        <div class="panel p-4"><Meter label={`vCPUs (host has ${lh.capacity.cpus})`} used={lh.vms.reduce((s, v) => s + v.cpus, 0)} cap={Math.max(1, lh.capacity.cpus * 2)} format={String} /></div>
        <div class="panel p-4"><Meter label="Disk free for VMs" used={lh.vms.reduce((s, v) => s + v.diskGiB, 0)} cap={Math.max(1, lh.capacity.diskGiB + lh.vms.reduce((s, v) => s + v.diskGiB, 0))} format={(n) => `${n} GiB`} /></div>
      </div>
      <div class="text-[12px] text-muted">{lh.capacity.hostname} · Debian, kernel {lh.capacity.kernel} · libvirt {lh.capacity.libvirt} · bridge {lh.capacity.bridge} · Talos boot assets {lh.talos} · checked {fmt.when(lh.capacity.checkedAt)}</div>
      <div class="panel">
        <div class="flex items-center gap-3 px-4 py-2.5 border-b border-border">
          <span class="font-medium">Virtual machines</span>
          <span class="ml-auto flex gap-2">
            <button class="btn btn-primary !py-1" disabled={lh.state !== 'ready'} onClick={() => setAdd(true)}>+ Add VMs…</button>
            <button class="btn btn-danger !py-1" onClick={() => setRelease(true)}>Release host…</button>
          </span>
        </div>
        <table class="data wrap">
          <thead><tr><th class="pl-4">VM</th><th>State</th><th>Size</th><th>Boot</th><th>Kubit</th><th></th></tr></thead>
          <tbody>
            {lh.vms.length === 0 && <tr><td colSpan={6} class="pl-4 py-3 text-muted">No VMs yet.</td></tr>}
            {lh.vms.map((vm) => {
              const row = rowOf(vm)
              return (
                <tr key={vm.name}>
                  <td class="pl-4"><span class="font-medium mono">{vm.name}</span><span class="block text-[11px] text-muted mono">{vm.mac}{vm.ip ? ` · ${vm.ip}` : ''}</span></td>
                  <td><Pill tone={vm.state === 'running' ? 'good' : 'muted'}>{vm.state}</Pill></td>
                  <td class="num">{vm.cpus} vCPU · {fmt.bytes(vm.memMiB << 20)} · {vm.diskGiB} GiB</td>
                  <td><Pill tone={vm.boot === 'disk' ? 'info' : 'muted'}>{vm.boot === 'disk' ? 'disk' : 'Talos (RAM)'}</Pill></td>
                  <td>{row ? (row.cluster ? <a class="text-accent hover:underline" href={`/clusters/${row.cluster}/nodes`}>{row.cluster} · {row.hostname}</a> : <Pill tone={row.state === 'maintenance' ? 'good' : 'warn'}>{row.state}</Pill>) : <span class="text-muted">—</span>}</td>
                  <td class="text-right pr-3 whitespace-nowrap">
                    {vm.state === 'running' ? <button class="btn !py-1" onClick={() => run(api.labVM(host.mac, vm.name, 'stop'))}>Stop</button> : <button class="btn !py-1" onClick={() => run(api.labVM(host.mac, vm.name, 'start'))}>Start</button>}
                    {' '}<button class="btn !py-1" disabled={!!row?.cluster} title={row?.cluster ? 'Remove it from the cluster first' : 'Boot Talos from RAM again (maintenance mode); the disk is wiped when it is next installed'} onClick={() => run(api.labVM(host.mac, vm.name, 'reprovision'))}>Re-provision</button>
                    {' '}<button class="btn btn-danger !py-1" disabled={!!row?.cluster} onClick={() => setDel(vm)}>Delete</button>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
      {add && <AddVMsDialog host={host} onClose={() => setAdd(false)} />}
      {del && <ConfirmDialog title={`Delete ${del.name}`} action="Delete VM" tone="danger" onClose={() => setDel(null)} onConfirm={() => api.labVMDelete(host.mac, del.name).then(() => setDel(null)).catch((e) => toast(e.message, 'error'))} impact={<p>Destroys the VM and its disk image on the host; its machine record is removed.</p>} />}
      {release && <ConfirmDialog title={`Release ${lh.capacity.hostname || host.hostname}`} action="Release host" tone="danger" typed={host.hostname || 'release'} onClose={() => setRelease(false)} onConfirm={() => api.labRelease(host.mac).then(() => setRelease(false)).catch((e) => toast(e.message, 'error'))} impact={<><p>Deletes every VM on the host (members are refused) and forgets the lab-host role. Debian stays installed; the machine can be re-provisioned or booted into Talos later.</p></>} />}
    </div>
  )
}

/** Wizard: lab hosts and the machines you can add there. */
export function LabHostsSection() {
  const hosts = machineList.value.filter((m) => m.labhost)
  const [add, setAdd] = useState<NodeRow | null>(null)
  if (hosts.length === 0) return null
  return (
    <div class="panel">
      <div class="flex items-center gap-3 px-4 py-2.5 border-b border-border"><span class="font-medium">Lab hosts</span><span class="text-[12px] text-muted">KVM hosts Kubit installed; VMs created here appear above as machines</span></div>
      <table class="data wrap">
        <thead><tr><th class="pl-4">Host</th><th>State</th><th>Capacity</th><th>VMs</th><th></th></tr></thead>
        <tbody>
          {hosts.map((h) => {
            const lh = h.labhost!
            const used = lh.vms.reduce((s, v) => s + v.memMiB, 0)
            return (
              <tr key={h.mac}>
                <td class="pl-4"><a class="font-medium hover:underline" href={`/machines/${h.mac}#labhost`}>{lh.capacity.hostname || h.hostname || h.mac}</a><span class="block text-[11px] text-muted mono">{h.ip} · {h.mac}</span></td>
                <td><Pill tone={lh.state === 'ready' ? 'good' : lh.state === 'error' ? 'bad' : 'warn'}>{lh.state}</Pill></td>
                <td class="num">{lh.capacity.cpus} CPU · {fmt.bytes(lh.capacity.memMiB << 20)} · {lh.capacity.diskGiB} GiB free</td>
                <td class="num">{lh.vms.length} ({fmt.bytes(used << 20)})</td>
                <td class="text-right pr-3"><button class="btn btn-primary !py-1" disabled={lh.state !== 'ready'} onClick={() => setAdd(h)}>+ Add VMs…</button></td>
              </tr>
            )
          })}
        </tbody>
      </table>
      {add && <AddVMsDialog host={add} onClose={() => setAdd(null)} />}
    </div>
  )
}

/** "Make lab host" confirm, used from the wizard's AMT rows and the machine page. */
export function MakeLabHostDialog({ m, onClose }: { m: NodeRow; onClose: () => void }) {
  return (
    <ConfirmDialog title={`Make ${m.hostname || m.ip} a lab host`} action="Install Debian + KVM" tone="danger" onClose={onClose}
      onConfirm={() => api.labProvision(m.mac).then((r) => { onClose(); watch(r) }).catch((e) => toast(e.message, 'error'))}
      impact={<ul class="list-disc pl-5 flex flex-col gap-1">
        <li class="text-bad">The machine's disk is wiped.</li>
        <li>AMT forces one network boot; <span class="mono">kubit pxe</span> must be running on this LAN for the install (about ten minutes, unattended).</li>
        <li>Debian with KVM/libvirt is installed with a bridge on the LAN; Kubit manages it over SSH with its own key.</li>
        <li>Afterwards you carve Talos VMs from it — they become ordinary machines for clusters.</li>
      </ul>} />
  )
}
