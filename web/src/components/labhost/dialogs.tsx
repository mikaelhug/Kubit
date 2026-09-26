import { useState } from 'preact/hooks'
import { api, fmt, type LabLocal, type LabVM, type NodeRow } from '../../api'
import { diskLabel, hostName, installCandidates, onMac, vmsOf } from '../../machine'
import { toast, watch } from '../../store'
import { useLive } from '../../useLive'
import { usePxeGated } from '../PxeGate'
import { Code, ConfirmDialog, Dialog, ErrorBox, Field, Meter, Notice } from '../ui'
import { MIN_VM_MIB, PlanFields, PREFERRED_MIB, RESERVED_MIB, VMTable, mib, planFor, reserveOf, rowsProblem, totalMem, usePlan, type VMRow } from './plan'

export function AddVMsDialog({ host, onClose }: { host: NodeRow; onClose: () => void }) {
  const lh = host.labhost!
  const used = totalMem(vmsOf(lh))
  const reserve = reserveOf(lh)
  const freeMiB = Math.max(0, lh.capacity.memMiB - reserve - used)
  const [rows, setRows] = useState<VMRow[]>(() => planFor(freeMiB + reserve, false, reserve, onMac(lh) ? PREFERRED_MIB : Infinity))
  const [error, setError] = useState<string | null>(null)
  const need = totalMem(rows)
  const over = need > freeMiB
  const overCpu = rows.reduce((s, v) => s + v.cpus, 0) > lh.capacity.cpus * 2
  const problem = rowsProblem(rows)
  const submit = () => api.labAddVMs(host.mac, { each: rows.map(({ key: _k, ...v }) => v) }).then((r) => { onClose(); watch(r) }).catch((e) => setError(e.message))
  return (
    <Dialog title={`Add VMs on ${lh.capacity.hostname || host.hostname}`} width="max-w-2xl" onClose={onClose} footer={<><button class="btn" onClick={onClose}>Cancel</button><button class="btn btn-primary" disabled={over || !!problem} onClick={submit}>Create {rows.length} VM{rows.length === 1 ? '' : 's'}</button></>}>
      <ErrorBox error={error} />
      <VMTable rows={rows} onChange={setRows} roles={false} />
      <Meter label={`Memory: ${fmt.bytes(mib(need))} of ${fmt.bytes(mib(freeMiB))} free`} used={need} cap={Math.max(freeMiB, 1)} format={(n) => fmt.bytes(mib(n))} />
      {over && <Notice tone="bad">Not enough memory.</Notice>}
      {!over && problem && <Notice tone="bad">{problem}.</Notice>}
      {overCpu && <Notice tone="warn">More than 2× the host's CPUs; the VMs will contend.</Notice>}
    </Dialog>
  )
}

export function ResizeVMDialog({ host, vm, onClose }: { host: NodeRow; vm: LabVM; onClose: () => void }) {
  const lh = host.labhost!
  const [cpus, setCpus] = useState(vm.cpus)
  const [memMiB, setMem] = useState(vm.memMiB)
  const [error, setError] = useState<string | null>(null)
  const others = totalMem(vmsOf(lh).filter((v) => v.name !== vm.name))
  const freeMiB = Math.max(0, lh.capacity.memMiB - reserveOf(lh) - others)
  const over = memMiB > freeMiB
  const small = memMiB < MIN_VM_MIB
  const submit = () => api.labVMResize(host.mac, vm.name, cpus, memMiB).then(onClose).catch((e) => setError(e.message))
  return (
    <Dialog title={`Resize ${vm.name}`} onClose={onClose} footer={<><button class="btn" onClick={onClose}>Cancel</button><button class="btn btn-primary" disabled={over || small || cpus < 1} onClick={submit}>Save</button></>}>
      <ErrorBox error={error} />
      <div class="grid grid-cols-2 gap-3">
        <Field label="vCPU"><input class="input" type="number" min={1} value={cpus} onInput={(e) => setCpus(Number((e.target as HTMLInputElement).value))} /></Field>
        <Field label="RAM (MiB)" hint={`${fmt.bytes(mib(freeMiB))} free for this VM`}><input class="input" type="number" min={MIN_VM_MIB} step={256} value={memMiB} onInput={(e) => setMem(Number((e.target as HTMLInputElement).value))} /></Field>
      </div>
      {over && <Notice tone="bad">Not enough memory.</Notice>}
      {small && <Notice tone="bad">Every VM needs at least {MIN_VM_MIB} MiB.</Notice>}
      <p class="text-[12px] text-muted">Applies when the VM next starts.</p>
    </Dialog>
  )
}

export function ReleaseHostDialog({ host, onClose }: { host: NodeRow; onClose: () => void }) {
  return <ConfirmDialog title={`Release ${hostName(host)}`} action="Release host" tone="danger" typed={host.hostname || 'release'} onClose={onClose} onConfirm={() => api.labRelease(host.mac).then(onClose).catch((e) => toast(e.message, 'error'))} impact={<p>{onMac(host.labhost) ? 'Deletes every VM and its disks, and removes this Mac from Inventory.' : 'Deletes every VM and drops the lab-host role; Debian stays on the disk.'}</p>} />
}

export function MakeLabHostDialog({ m, onClose }: { m: NodeRow; onClose: () => void }) {
  const memMiB = Math.floor((m.inventory?.memoryBytes ?? 0) / (1 << 20))
  const p = usePlan(memMiB, RESERVED_MIB)
  const disks = installCandidates(m)
  const [disk, setDisk] = useState(disks[0]?.devPath ?? '')
  const [error, setError] = useState<string | null>(null)
  const manual = !m.oobType
  const plan = { manual: manual || undefined, disk: disk || undefined, ...p.body }
  const gated = usePxeGated(() => api.labProvision(m.mac, plan), (r) => { onClose(); watch(r) }, (msg) => setError(msg))
  if (gated.element) return gated.element
  if (m.host || m.cluster) {
    return (
      <Dialog title={`Make ${m.hostname || m.ip} a lab host`} onClose={onClose} footer={<button class="btn" onClick={onClose}>Close</button>}>
        <Notice tone="muted">{m.host ? 'A lab VM cannot host VMs.' : 'Remove it from the cluster first.'}</Notice>
      </Dialog>
    )
  }
  return (
    <Dialog title={`Make ${m.hostname || m.ip} a lab host`} width="max-w-2xl" onClose={onClose} footer={<><button class="btn" onClick={onClose}>Cancel</button><button class="btn btn-primary" disabled={p.blocked} onClick={() => gated.attempt('Make lab host')}>{p.withVMs && p.withCluster ? 'Install and create cluster' : p.withVMs ? 'Install and add VMs' : 'Install'}</button></>}>
      <ErrorBox error={error} />
      <p class="text-[13px]"><span class="text-bad">Wipes the install disk</span> and installs Debian with KVM (about 10 min{manual ? '; you boot the installer' : ''}).</p>
      <Field label="Install disk" hint={disks.length > 1 ? 'Holds Debian and the VM images' : disks.length === 1 ? 'The only disk' : 'Unknown until Talos boots once; the installer takes the largest'}>
        {disks.length > 1
          ? <select class="input mono" value={disk} onChange={(e) => setDisk((e.target as HTMLSelectElement).value)}>
              {disks.map((d) => <option key={d.devPath} value={d.devPath}>{diskLabel(d)}</option>)}
            </select>
          : <div class="input mono text-muted">{disks[0] ? diskLabel(disks[0]) : 'largest disk'}</div>}
      </Field>
      <PlanFields p={p} keeps="host" />
    </Dialog>
  )
}

export function ThisMacDialog({ onClose }: { onClose: () => void }) {
  const { data: info, error, reload } = useLive(() => api.labLocal(), [])
  if (info?.supported && !info.problem && !info.host && info.capacity) return <ThisMacPlan info={info} onClose={onClose} />
  return (
    <Dialog title="Lab host on this Mac" onClose={onClose} footer={<><button class="btn" onClick={onClose}>Close</button>{info?.supported && info.problem && <button class="btn btn-primary" onClick={reload}>Check again</button>}</>}>
      <ErrorBox error={error} />
      {!info && !error && <p class="text-[13px] text-muted">Checking this Mac</p>}
      {info?.host && <Notice tone="muted">This Mac is already a lab host. <a class="underline" href={`/labhosts/${info.host}/overview`}>Open it</a></Notice>}
      {info && !info.host && info.problem && <Notice tone={info.supported ? 'bad' : 'muted'}>{info.problem}</Notice>}
      {info && !info.host && info.command && <Code text={info.command} />}
    </Dialog>
  )
}

function ThisMacPlan({ info, onClose }: { info: LabLocal; onClose: () => void }) {
  const capa = info.capacity!
  const p = usePlan(capa.memMiB, capa.reserveMiB || RESERVED_MIB, 'mac', PREFERRED_MIB)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const submit = () => { setBusy(true); api.labLocalCreate(p.body).then((r) => { onClose(); watch(r) }).catch((e) => { setBusy(false); setError(e.message) }) }
  return (
    <Dialog title="Lab host on this Mac" width="max-w-2xl" onClose={onClose} footer={<><button class="btn" onClick={onClose}>Cancel</button><button class="btn btn-primary" disabled={p.blocked || busy} onClick={submit}>{p.withVMs && p.withCluster ? 'Create VMs and cluster' : p.withVMs ? 'Create VMs' : 'Set up'}</button></>}>
      <ErrorBox error={error} />
      <p class="text-[13px]">Talos VMs under vfkit, on {info.subnet}.</p>
      <PlanFields p={p} keeps="this Mac" />
    </Dialog>
  )
}
