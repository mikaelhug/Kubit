import { useState } from 'preact/hooks'
import { api, fmt, type LabLocal, type LabVM, type NodeRow } from '../../api'
import { diskLabel, hostName, installCandidates, labOffline, onMac, vmsOf } from '../../machine'
import { defaultDebian, hostDisks, inventoryDisks, invKey, storagePlan, withSlots } from '../../labdisks'
import { opEvents, operations, running, watch } from '../../ops'
import { machineKey, machines, toast } from '../../store'
import { useLive } from '../../useLive'
import { usePxeGated } from '../PxeGate'
import { Elapsed } from '../Time'
import { Code, ConfirmDialog, Dialog, ErrorBox, Field, Meter, Notice } from '../ui'
import { ADD_EPH_GIB } from '../../labsizing'
import { MAC_MAX_MIB, MIN_VM_MIB, PlanFields, RESERVED_MIB, SizingNotes, StorageNotes, VMTable, mib, reserveOf, rowsProblem, totalMem, usePlan, useSuggested } from './plan'

export function AddVMsDialog({ host, onClose }: { host: NodeRow; onClose: () => void }) {
  const lh = host.labhost!
  const used = totalMem(vmsOf(lh))
  const reserve = reserveOf(lh)
  const freeMiB = Math.max(0, lh.capacity.memMiB - reserve - used)
  const mac = onMac(lh)
  const known = !mac && (lh.capacity.disks?.length ?? 0) > 0 ? hostDisks(lh) : undefined
  const choices = !!known?.some((d) => d.use === 'free' || d.use === 'pool')
  const sug = useSuggested({ memMiB: freeMiB, cpus: lh.capacity.cpus, vcpusUsed: vmsOf(lh).reduce((t, v) => t + v.cpus, 0), disks: known, macFreeGiB: mac ? lh.capacity.diskGiB : undefined, cluster: false, mac, most: mac ? MAC_MAX_MIB : undefined, eph: ADD_EPH_GIB })
  const rows = sug.rows
  const [error, setError] = useState<string | null>(null)
  const need = totalMem(rows)
  const over = need > freeMiB
  const overCpu = rows.reduce((s, v) => s + v.cpus, 0) + vmsOf(lh).reduce((t, v) => t + v.cpus, 0) > lh.capacity.cpus * 2
  const problem = rowsProblem(rows) ?? (known ? storagePlan(rows, known).problem : null)
  const each = rows.map(({ key: _k, ...v }) => v)
  const submit = () => api.labAddVMs(host.mac, { each: choices && known ? withSlots(each, known) : each.map(({ systemDisk: _s, dataDisk: _d, ...v }) => v) }).then((r) => { onClose(); watch(r) }).catch((e) => setError(e.message))
  return (
    <Dialog title={`Add VMs on ${lh.capacity.hostname || host.hostname}`} width={choices ? 'max-w-4xl' : 'max-w-2xl'} onClose={onClose} footer={<><button class="btn" onClick={onClose}>Cancel</button><button class="btn btn-primary" disabled={over || !!problem} onClick={submit}>Create {rows.length} VM{rows.length === 1 ? '' : 's'}</button></>}>
      <ErrorBox error={error} />
      <VMTable rows={rows} onChange={sug.setRows} roles={false} disks={choices ? known : undefined} onSuggest={sug.edited ? sug.reset : undefined} />
      <SizingNotes s={sug.s} edited={sug.edited} count={rows.length} />
      <StorageNotes rows={rows} disks={known} />
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
  const offline = labOffline(host.labhost)
  const release = () => api.labRelease(host.mac).then((r) => { if (r?.warning) toast(r.warning); onClose() }).catch((e) => toast(e.message, 'error'))
  return <ConfirmDialog title={`Release ${hostName(host)}`} action="Release host" tone="danger" typed={host.hostname || 'release'} onClose={onClose} onConfirm={release} impact={<p>{offline ? 'The host is offline: Kubit forgets its VMs; they keep running on the host.' : onMac(host.labhost) ? 'Deletes every VM and its disks, and removes this Mac from Inventory.' : 'Deletes every VM and drops the lab-host role; Debian stays on the disk.'}</p>} />
}

export function MakeLabHostDialog({ m: initial, onClose }: { m: NodeRow; onClose: () => void }) {
  const m = machines.value.get(machineKey(initial)) ?? initial
  const memMiB = Math.floor((m.inventory?.memoryBytes ?? 0) / (1 << 20))
  const cands = installCandidates(m)
  const [chosen, setDebian] = useState<string | null>(null)
  const debian = chosen ?? defaultDebian(cands)
  const disks = cands.length > 0 ? inventoryDisks(cands, debian) : undefined
  const p = usePlan({ memMiB, reserve: RESERVED_MIB, cpus: m.inventory?.cpus ?? 0, name: 'lab', disks, choices: cands.length > 1 })
  const [error, setError] = useState<string | null>(null)
  const manual = !m.oobType
  const plan = { manual: manual || undefined, disk: debian || undefined, ...p.body }
  const gated = usePxeGated(() => api.labProvision(m.mac, plan), (r) => { onClose(); watch(r) }, (msg) => setError(msg))
  const [scanID, setScanID] = useState<number | null>(null)
  const scanGate = usePxeGated(() => api.power(m.mac, 'pxe'), (r) => { setScanID(r.operationId); watch(r, false) }, (msg) => setError(msg))
  const scanOp = scanID != null ? operations.value.get(scanID) : running.value.find((o) => o.kind === 'machine.power' && (o.request as { mac?: string } | undefined)?.mac === m.mac)
  const scanning = scanOp ? scanOp.status === 'running' : scanID != null
  const scanLine = scanOp ? opEvents.value.get(scanOp.id)?.at(-1)?.message : undefined
  if (gated.element) return gated.element
  if (scanGate.element) return scanGate.element
  if (m.host || m.cluster) {
    return (
      <Dialog title={`Make ${m.hostname || m.ip} a lab host`} onClose={onClose} footer={<button class="btn" onClick={onClose}>Close</button>}>
        <Notice tone="muted">{m.host ? 'A lab VM cannot host VMs.' : 'Remove it from the cluster first.'}</Notice>
      </Dialog>
    )
  }
  return (
    <Dialog title={`Make ${m.hostname || m.ip} a lab host`} width={p.choices ? 'max-w-4xl' : 'max-w-2xl'} onClose={onClose} footer={<><button class="btn" onClick={onClose}>Cancel</button><button class="btn btn-primary" disabled={p.blocked || scanning} onClick={() => gated.attempt('Make lab host')}>{p.withVMs && p.withCluster ? 'Install and create cluster' : p.withVMs ? 'Install and add VMs' : 'Install'}</button></>}>
      <ErrorBox error={error} />
      <p class="text-[13px]"><span class="text-bad">Wipes {disks?.find((d) => d.use === 'os')?.name ?? 'the install disk'}</span> and installs Debian with KVM (about 10 min{manual ? '; you boot the installer' : ''}).</p>
      <Field label="Debian disk" hint={cands.length > 1 ? 'VM disks are chosen per VM below' : cands.length === 1 ? 'The only disk; it also holds the VM images' : m.oobType ? 'Unknown until Talos boots once' : 'Unknown until Talos boots once; the installer takes the largest'}>
        {cands.length > 1
          ? <select class="input mono" value={debian} onChange={(e) => { setDebian((e.target as HTMLSelectElement).value); p.reset() }}>
              {cands.map((d) => <option key={invKey(d)} value={invKey(d)}>{diskLabel(d)}</option>)}
            </select>
          : cands.length === 1
            ? <div class="input mono text-muted">{diskLabel(cands[0])}</div>
            : <div class="flex items-center gap-2"><div class="input mono text-muted flex-1">largest disk</div>{m.oobType && <button class="btn" disabled={scanning} title="Boots Talos once to read the disks" onClick={() => scanGate.attempt('Scan disks')}>{scanning ? 'Scanning' : 'Scan disks'}</button>}</div>}
      </Field>
      {cands.length === 0 && scanning && (
        <Notice tone="info">
          <span class="flex flex-col gap-0.5">
            <span class="flex items-center gap-2"><span class="inline-block h-1.5 w-1.5 rounded-full bg-accent animate-pulse" />Scanning disks · {scanOp ? <Elapsed from={scanOp.startedAt} /> : 'starting'}</span>
            {scanLine && <span class="text-[12px] text-muted truncate" title={scanLine}>{scanLine}</span>}
          </span>
        </Notice>
      )}
      {cands.length === 0 && scanOp?.status === 'failed' && <Notice tone="bad">Disk scan failed{scanLine ? `: ${scanLine}` : '.'}</Notice>}
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
  const p = usePlan({ memMiB: capa.memMiB, reserve: capa.reserveMiB || RESERVED_MIB, cpus: capa.cpus, name: 'mac', macFreeGiB: capa.diskGiB, mac: true, most: MAC_MAX_MIB })
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
