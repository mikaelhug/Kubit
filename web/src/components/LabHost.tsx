import type { ComponentChildren } from 'preact'
import { useEffect, useState } from 'preact/hooks'
import { api, fmt, labHostKey, labNeedsReboot, vmsOf, type LabHost, type LabLocal, type LabVM, type NodeRow, type Sample, type VMSize } from '../api'
import { ack, health, hostSamples, loadHealth, machineList, toast, watch } from '../store'
import { Code, ConfirmDialog, Dialog, ErrorBox, Field, MaintenanceNotice, Meter, Notice, Pill } from './ui'
import { usePxeGated } from './PxeGate'
import { hostName, hostOf, installCandidates, KindPill, labOffline, onMac } from '../machine'
import { Sparkline } from './Sparkline'
import { EventRow } from '../pages/cluster/Overview'
import { DataTable, type Column } from './DataTable'

const RESERVED_MIB = 2048
export const reserveOf = (lh?: LabHost | null) => lh?.capacity.reserveMiB || RESERVED_MIB
// MiB to bytes without the 32-bit `<<` that wraps at 2 GiB.
const mib = (n: number) => n * 1048576

type VMRow = VMSize & { key: number }
let vmKey = 0
const defaultVM = (role: VMSize['role'], mem = 3072): VMRow => ({ key: ++vmKey, role, cpus: 2, memMiB: Math.max(mem, MIN_VM_MIB), diskGiB: 60, dataGiB: 0 })
export const MIN_CP_MIB = 2048
export const MIN_VM_MIB = 2048
const PREFERRED_MIB = 3072

/** VMs that fit the host: as many 3 GiB VMs as fit, else fewer, larger ones; first is the control plane. */
export function planFor(hostMiB: number, cluster = true, reserve = RESERVED_MIB, most = Infinity): VMRow[] {
  if (hostMiB <= 0) return [defaultVM('controlplane'), defaultVM('worker'), defaultVM('worker'), defaultVM('worker')]
  const avail = hostMiB - reserve
  let n = Math.min(4, Math.floor(avail / PREFERRED_MIB))
  if (n < 2) n = Math.min(4, Math.floor(avail / MIN_VM_MIB))
  if (n < 1) return [defaultVM('controlplane', MIN_VM_MIB)]
  const each = Math.min(most, Math.floor(avail / n / 256) * 256)
  return Array.from({ length: n }, (_, i) => defaultVM(cluster && i === 0 ? 'controlplane' : 'worker', each))
}

/** One editable row per VM. Every VM gets at least 2 GiB; roles matter only when a cluster is planned. */
export function VMTable({ rows, onChange, roles }: { rows: VMRow[]; onChange: (rows: VMRow[]) => void; roles: boolean }) {
  const set = (i: number, patch: Partial<VMSize>) => onChange(rows.map((r, j) => (j === i ? { ...r, ...patch } : r)))
  const num = (i: number, k: 'cpus' | 'memMiB' | 'diskGiB' | 'dataGiB', min: number, step = 1) => (
    <input class="input num !py-1 w-full" type="number" min={min} step={step} value={rows[i][k]} onInput={(e) => set(i, { [k]: Number((e.target as HTMLInputElement).value) })} />
  )
  const columns: Column<number>[] = [
    { id: 'n', header: '#', width: '2rem', cell: (i) => <span class="text-muted num">{i + 1}</span> },
    ...(roles ? [{ id: 'role', header: 'Role', width: '9rem', cell: (i: number) => { const r = rows[i]; return <select class="input !py-1 w-full" value={r.role} onChange={(e) => { const role = (e.target as HTMLSelectElement).value as VMSize['role']; set(i, { role, memMiB: role === 'controlplane' ? Math.max(r.memMiB, MIN_CP_MIB) : r.memMiB }) }}><option value="controlplane">control plane</option><option value="worker">worker</option></select> } }] : []),
    { id: 'cpus', header: 'vCPU', width: '5rem', cell: (i) => num(i, 'cpus', 1) },
    { id: 'mem', header: 'RAM (MiB)', width: '7rem', cell: (i) => num(i, 'memMiB', MIN_VM_MIB, 256) },
    { id: 'disk', header: 'Disk (GiB)', width: '6rem', cell: (i) => num(i, 'diskGiB', 8) },
    { id: 'data', header: <span title="0 = none; mounted at /var/mnt/data-1">Data (GiB)</span>, width: '6rem', cell: (i) => num(i, 'dataGiB', 0, 10) },
    { id: 'remove', header: '', width: '2rem', align: 'right', cell: (i) => <button class="btn !px-2 !py-1" title="Remove" disabled={rows.length <= 1} onClick={() => onChange(rows.filter((_, j) => j !== i))}>✕</button> },
  ]
  return (
    <div class="flex flex-col gap-2">
      <DataTable search={false} columns={columns} rows={rows.map((_, i) => i)} rowKey={(i) => String(rows[i].key)} />
      <div><button class="btn !py-1" onClick={() => onChange([...rows, defaultVM('worker', rows[rows.length - 1]?.memMiB)])}>+ Add VM</button></div>
    </div>
  )
}

const totalMem = (rows: VMSize[]) => rows.reduce((s, v) => s + v.memMiB, 0)
const cpCount = (rows: VMSize[]) => rows.filter((v) => v.role === 'controlplane').length

/** Mirrors the server's per-VM floors so the dialog blocks a plan the API would 400. */
const rowsProblem = (rows: VMSize[]): string | null => {
  for (const v of rows) {
    if (v.cpus < 1) return 'every VM needs at least 1 vCPU'
    if (v.diskGiB < 8) return 'every VM needs at least 8 GiB disk'
    if (v.memMiB < MIN_VM_MIB) return 'every VM needs at least 2048 MiB'
  }
  return null
}

/** Add VMs to a running lab host. */
export function AddVMsDialog({ host, onClose }: { host: NodeRow; onClose: () => void }) {
  const lh = host.labhost!
  const vms = vmsOf(lh)
  const used = vms.reduce((s, v) => s + v.memMiB, 0)
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

/** The VMs of a lab host: state, size, boot source, cluster membership, and the per-VM operations. */
export function HostVMs({ host }: { host: NodeRow }) {
  const lh = host.labhost
  const [add, setAdd] = useState(false)
  const [del, setDel] = useState<LabVM | null>(null)
  const [resize, setResize] = useState<LabVM | null>(null)
  if (!lh) return null
  const vms = vmsOf(lh)
  const rows = machineList.value
  const rowOf = (vm: LabVM) => rows.find((m) => m.mac === vm.mac)
  const run = (p: Promise<{ operationId: number }>) => p.then((r) => watch(r, false)).catch((e) => toast(e.message, 'error'))
  const offline = labOffline(lh)
  const columns: Column<LabVM>[] = [
    { id: 'vm', header: 'VM', cell: (vm) => { const row = rowOf(vm); return (
      <span class="flex flex-col min-w-0">
        {row ? <a class="font-medium mono hover:underline" href={`/machines/${row.mac}`}>{vm.name}</a> : <span class="font-medium mono">{vm.name}</span>}
        <span class="text-[10px] text-muted mono">{vm.mac}{row?.ip || vm.ip ? ` · ${row?.ip || vm.ip}` : ''}</span>
      </span>
    ) } },
    { id: 'state', header: 'State', cell: (vm) => <Pill tone={offline ? 'muted' : vm.state === 'running' ? 'good' : 'muted'}>{vm.state}</Pill> },
    { id: 'size', header: 'Size', cell: (vm) => <span class="num">{vm.cpus} vCPU · {fmt.bytes(mib(vm.memMiB))} · {vm.diskGiB} GiB{vm.dataGiB ? ` + ${vm.dataGiB} GiB data` : ''}</span> },
    { id: 'boot', header: 'Boot', cell: (vm) => <Pill tone={vm.boot === 'disk' ? 'info' : 'muted'}>{vm.boot === 'disk' ? 'disk' : onMac(lh) ? 'Talos ISO' : 'Talos (RAM)'}</Pill> },
    { id: 'kubit', header: 'Kubit', cell: (vm) => { const row = rowOf(vm); return row ? (row.cluster ? <a class="text-accent hover:underline" href={`/clusters/${row.cluster}/nodes`}>{row.cluster} · {row.hostname}</a> : <KindPill m={row} />) : <span class="text-muted">—</span> } },
    { id: 'actions', header: '', align: 'right', cell: (vm) => { const member = !!rowOf(vm)?.cluster; return (
      <span class="flex gap-1 justify-end">
        {vm.state === 'running' ? <button class="btn !py-1" disabled={member || offline} title={member ? 'Drain and remove it from the cluster first' : ''} onClick={() => run(api.labVM(host.mac, vm.name, 'stop'))}>Stop</button> : <button class="btn !py-1" disabled={offline} onClick={() => run(api.labVM(host.mac, vm.name, 'start'))}>Start</button>}
        <button class="btn !py-1" disabled={offline} onClick={() => setResize(vm)}>Resize</button>
        <button class="btn !py-1" disabled={member || offline} title={member ? 'Remove it from the cluster first' : 'Boot back into Talos maintenance mode'} onClick={() => run(api.labVM(host.mac, vm.name, 'reprovision'))}>Re-provision</button>
        <button class="btn btn-danger !py-1" disabled={member || offline} onClick={() => setDel(vm)}>Delete</button>
      </span>
    ) } },
  ]
  return (
    <div class="flex flex-col gap-4">
      <DataTable search={false} columns={columns} rows={vms} rowKey={(vm) => vm.name} empty="No VMs yet."
        title={<><span class="font-medium">Virtual machines</span><span class="text-[12px] text-muted">{fmt.bytes(mib(vms.reduce((s, v) => s + v.memMiB, 0)))} of {fmt.bytes(mib(Math.max(0, lh.capacity.memMiB - reserveOf(lh))))} assigned</span></>}
        toolbar={<button class="btn btn-primary !py-1" disabled={lh.state !== 'ready' || offline} onClick={() => setAdd(true)}>+ Add VMs</button>} />
      {add && <AddVMsDialog host={host} onClose={() => setAdd(false)} />}
      {resize && <ResizeVMDialog host={host} vm={resize} onClose={() => setResize(null)} />}
      {del && <ConfirmDialog title={`Delete ${del.name}`} action="Delete VM" tone="danger" onClose={() => setDel(null)} onConfirm={() => api.labVMDelete(host.mac, del.name).then(() => setDel(null)).catch((e) => toast(e.message, 'error'))} impact={<p>Destroys the VM and its disk.</p>} />}
    </div>
  )
}

/** Change vCPU and memory of one VM; takes effect on its next boot. */
function ResizeVMDialog({ host, vm, onClose }: { host: NodeRow; vm: LabVM; onClose: () => void }) {
  const lh = host.labhost!
  const [cpus, setCpus] = useState(vm.cpus)
  const [memMiB, setMem] = useState(vm.memMiB)
  const [error, setError] = useState<string | null>(null)
  const others = vmsOf(lh).filter((v) => v.name !== vm.name).reduce((s, v) => s + v.memMiB, 0)
  const freeMiB = Math.max(0, lh.capacity.memMiB - reserveOf(lh) - others)
  const over = memMiB > freeMiB
  const small = memMiB < MIN_VM_MIB
  const submit = () => api.labVMResize(host.mac, vm.name, cpus, memMiB).then(onClose).catch((e) => setError(e.message))
  return (
    <Dialog title={`Resize ${vm.name}`} onClose={onClose} footer={<><button class="btn" onClick={onClose}>Cancel</button><button class="btn btn-primary" disabled={over || small || cpus < 1} onClick={submit}>Save</button></>}>
      <ErrorBox error={error} />
      <div class="grid grid-cols-2 gap-3">
        <Field label="vCPU"><input class="input num" type="number" min={1} value={cpus} onInput={(e) => setCpus(Number((e.target as HTMLInputElement).value))} /></Field>
        <Field label="RAM (MiB)" hint={`${fmt.bytes(mib(freeMiB))} free for this VM`}><input class="input num" type="number" min={MIN_VM_MIB} step={256} value={memMiB} onInput={(e) => setMem(Number((e.target as HTMLInputElement).value))} /></Field>
      </div>
      {over && <Notice tone="bad">Not enough memory.</Notice>}
      {small && <Notice tone="bad">Every VM needs at least {MIN_VM_MIB} MiB.</Notice>}
      <p class="text-[12px] text-muted">Applies {onMac(lh) ? 'when the VM next starts' : 'on the next boot of the VM'}{vm.state === 'running' ? '; it keeps running until then' : ''}.</p>
    </Dialog>
  )
}

/** Install progress, boot line, maintenance and failure notices for a lab host. */
export function HostStateNotice({ host }: { host: NodeRow }) {
  const lh = host.labhost
  const [retry, setRetry] = useState(false)
  if (!lh) return null
  return (
    <>
      {lh.state === 'error' && <Notice tone="bad"><span class="flex items-center gap-3">Lab host setup failed: {lh.error}{!onMac(lh) && <button class="btn !py-1 ml-auto shrink-0" onClick={() => setRetry(true)}>Make lab host</button>}</span></Notice>}
      {retry && <MakeLabHostDialog m={host} onClose={() => setRetry(false)} />}
      {lh.state === 'installing' && <Notice tone="warn">Installing Debian{lh.install ? <> · {installStage(lh.install.stage)} · {fmt.when(lh.install.at)}</> : ' · waiting for the machine to boot the installer'}. Details in Activity.</Notice>}
      {lh.state === 'installing' && !lh.install && lh.boot && (
        <div class="panel p-3 flex flex-col gap-2">
          <span class="label">Boot the installer with</span>
          <div class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-[12px] items-center">
            <span class="text-muted">kernel</span><Code text={lh.boot.kernel} />
            <span class="text-muted">initrd</span><Code text={lh.boot.initrd} />
            <span class="text-muted">cmdline</span><Code text={lh.boot.cmdline} />
          </div>
        </div>
      )}
      {lh.state === 'setup' && <Notice tone="warn">{onMac(lh) ? 'Checking vfkit and fetching the Talos ISO.' : 'Debian is installed; verifying KVM and fetching Talos boot assets.'}</Notice>}
      {lh.state === 'updating' && <Notice tone="warn">Host maintenance running; progress is in the Activity drawer.</Notice>}
    </>
  )
}

/** Release confirmation, shared by the machine page's Actions tab. */
export function ReleaseHostDialog({ host, onClose }: { host: NodeRow; onClose: () => void }) {
  return <ConfirmDialog title={`Release ${hostName(host)}`} action="Release host" tone="danger" typed={host.hostname || 'release'} onClose={onClose} onConfirm={() => api.labRelease(host.mac).then(onClose).catch((e) => toast(e.message, 'error'))} impact={<p>{onMac(host.labhost) ? 'Deletes every VM and its disks, and removes this Mac from Inventory.' : 'Deletes every VM and drops the lab-host role. Debian stays on the disk.'}</p>} />
}

/** Start, stop, re-provision and delete for one lab VM, driven through its host. */
export function LabVMControls({ vm }: { vm: NodeRow }) {
  const host = hostOf(vm)
  const entry = vmsOf(host?.labhost).find((v) => v.mac === vm.mac)
  const [del, setDel] = useState(false)
  if (!host || !entry) return <Notice tone="muted">Its lab host is no longer known.</Notice>
  const run = (p: Promise<{ operationId: number }>) => p.then((r) => watch(r, false)).catch((e) => toast(e.message, 'error'))
  const member = !!vm.cluster
  return (
    <div class="panel p-3 flex items-center gap-4">
      <div class="flex-1 min-w-0">
        <div class="font-medium">VM on <a class="text-accent hover:underline" href={`/labhosts/${host.mac}/overview`}>{hostName(host)}</a></div>
        <p class="text-[12.5px] text-muted">{entry.cpus} vCPU · {fmt.bytes(mib(entry.memMiB))} · {entry.diskGiB} GiB · {entry.state}{entry.boot === 'disk' ? ' · boots from disk' : onMac(host.labhost) ? ' · boots the Talos ISO' : ' · boots Talos over the network'}</p>
      </div>
      <div class="flex gap-2 shrink-0">
        {entry.state === 'running' ? <button class="btn" disabled={member} title={member ? 'Drain and remove it from the cluster first' : ''} onClick={() => run(api.labVM(host.mac, entry.name, 'stop'))}>Stop</button> : <button class="btn btn-primary" onClick={() => run(api.labVM(host.mac, entry.name, 'start'))}>Start</button>}
        <button class="btn" disabled={member} title={member ? 'Remove it from the cluster first' : 'Boot back into Talos maintenance mode'} onClick={() => run(api.labVM(host.mac, entry.name, 'reprovision'))}>Re-provision</button>
        <button class="btn btn-danger" disabled={member} onClick={() => setDel(true)}>Delete</button>
      </div>
      {del && <ConfirmDialog title={`Delete ${entry.name}`} action="Delete VM" tone="danger" onClose={() => setDel(false)} onConfirm={() => api.labVMDelete(host.mac, entry.name).then(() => setDel(false)).catch((e) => toast(e.message, 'error'))} impact={<p>Destroys the VM and its disk.</p>} />}
    </div>
  )
}

/** Installer stages as the operator reads them. */
export function installStage(stage: string) {
  return ({ installer: 'installer started', partitioning: 'partitioning', packages: 'packages installed', 'late-done': 'rebooting', booted: 'booted into Debian' } as Record<string, string>)[stage] ?? stage
}

/** Open alerts for the host, from the same watcher path clusters use. */
export function HostAlerts({ mac }: { mac: string }) {
  const key = labHostKey(mac)
  useEffect(() => { loadHealth(key) }, [key])
  const alerts = (health.value.get(key) ?? []).filter((e) => !e.acked && e.severity !== 'info')
  if (alerts.length === 0) return null
  return (
    <div class="panel border-warn/50">
      <div class="flex items-center gap-2 px-4 py-2 border-b border-border">
        <span class="font-semibold">{alerts.length} active alert{alerts.length === 1 ? '' : 's'}</span>
        <button class="btn !py-0.5 !px-2 text-[12px] ml-auto" onClick={() => ack(key)}>Acknowledge all</button>
      </div>
      {alerts.map((e) => <EventRow key={e.id} e={e} onAck={() => ack(key, e.id)} />)}
    </div>
  )
}

/** CPU, memory, VM disk and VM count with history; the newest reading arrives live. */
export function HostMetrics({ host, lh }: { host: NodeRow; lh: LabHost }) {
  const vms = vmsOf(lh)
  const [range, setRange] = useState('24h')
  const [samples, setSamples] = useState<Sample[]>([])
  useEffect(() => { api.labSamples(host.mac, range).then(setSamples).catch(() => {}) }, [host.mac, range])
  const live = hostSamples.value.get(host.mac)
  useEffect(() => {
    if (!live) return
    setSamples((prev) => (prev.length && prev[prev.length - 1].ts >= live.ts ? prev : [...prev, live]))
  }, [live?.ts]) // eslint-disable-line
  const m = lh.metrics
  const pts = (f: (s: Sample) => number) => samples.map((s) => ({ t: new Date(s.ts).getTime(), v: s.reachable ? f(s) : null }))
  const committed = vms.reduce((s, v) => s + v.diskGiB, 0)
  const diskPct = m && m.diskTotal ? fmt.pct(m.diskUsed, m.diskTotal) : 0
  const memPct = m && m.memTotal ? fmt.pct(m.memUsed, m.memTotal) : 0
  const tone = (pct: number, warn: number, bad: number) => (pct >= bad ? 'text-bad' : pct >= warn ? 'text-warn' : '')
  const offline = labOffline(lh)
  const graph = offline ? 'bad' : 'accent'
  const stale = (cls: string) => (offline ? 'text-muted' : cls)
  return (
    <div class="panel p-3 flex flex-col gap-3">
      <div class="flex items-center gap-2">
        <span class="label">Host utilisation</span>
        {m && !offline && <span class="text-[12px] text-muted">load {m.load1.toFixed(2)} · up {fmt.uptime(m.uptimeSec)}</span>}
        <div class="ml-auto flex gap-1">
          {['1h', '6h', '24h', '7d'].map((r) => <button key={r} class={`btn !py-0.5 !px-2 text-[11px] ${r === range ? 'border-accent text-accent' : ''}`} onClick={() => setRange(r)}>{r}</button>)}
        </div>
      </div>
      <div class="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-4 gap-4">
        <MetricCard label="CPU" value={m ? `${Math.round(m.cpuPct)}%` : '—'} sub={`${lh.capacity.cpus} cores · ${vms.reduce((s, v) => s + v.cpus, 0)} vCPU assigned`} cls={stale(m ? tone(m.cpuPct, 80, 95) : '')}>
          <Sparkline label="" points={pts((s) => s.cpuMilli / 10)} max={100} format={(v) => `${Math.round(v)}%`} height={44} tone={graph} />
        </MetricCard>
        <MetricCard label="Memory" value={m ? fmt.bytes(m.memUsed) : '—'} sub={m ? `of ${fmt.bytes(m.memTotal)} · ${fmt.bytes(mib(vms.reduce((s, v) => s + v.memMiB, 0)))} assigned to VMs` : ''} cls={stale(tone(memPct, 85, 92))}>
          <Sparkline label="" points={pts((s) => s.memBytes)} max={m?.memTotal} format={fmt.bytes} height={44} tone={graph} />
        </MetricCard>
        <MetricCard label="VM disk" value={m ? `${diskPct}%` : '—'} sub={m ? `${fmt.bytes(m.diskUsed)} of ${fmt.bytes(m.diskTotal)} · VMs may grow to ${committed} GiB` : ''} cls={stale(tone(diskPct, 85, 95))}>
          <Sparkline label="" points={pts((s) => s.disk ?? 0)} max={m?.diskTotal} format={fmt.bytes} height={44} tone={graph} />
        </MetricCard>
        <MetricCard label="VMs running" value={offline ? '—' : m ? `${m.vmsRunning}/${vms.length}` : `${vms.filter((v) => v.state === 'running').length}/${vms.length}`} sub={`${vms.length} defined on this host`} cls={stale('')}>
          <Sparkline label="" points={pts((s) => s.pods)} max={Math.max(1, vms.length)} format={String} height={44} tone={graph} />
        </MetricCard>
      </div>
      <span class="text-[11px] text-muted">{onMac(lh) ? 'Read' : 'Read over SSH'} once a minute; kept 24 h, then hourly for 30 d. Gaps mean the host did not answer.</span>
    </div>
  )
}

function MetricCard({ label, value, sub, cls, children }: { label: string; value: string; sub?: string; cls?: string; children: ComponentChildren }) {
  return (
    <div class="flex flex-col gap-1 min-w-0">
      <span class="label">{label}</span>
      <span class={`text-2xl font-semibold num truncate ${cls || ''}`}>{value}</span>
      {sub && <span class="text-[12px] text-muted truncate" title={sub}>{sub}</span>}
      {children}
    </div>
  )
}

/** Debian, kernel, pending updates, and the two host operations. */
export function HostSystem({ host, lh, busy }: { host: NodeRow; lh: LabHost; busy: boolean }) {
  const vms = vmsOf(lh)
  const u = lh.updates
  const reboot = labNeedsReboot(u)
  const [confirm, setConfirm] = useState<'update' | 'reboot' | null>(null)
  const [checking, setChecking] = useState(false)
  const clusters = [...new Set(vms.map((vm) => machineList.value.find((m) => m.mac === vm.mac)?.cluster).filter((c): c is string => !!c))]
  const running = vms.filter((v) => v.state === 'running').length
  const check = () => { setChecking(true); api.labCheck(host.mac).catch((e) => toast(e.message, 'error')).finally(() => setChecking(false)) }
  const start = (p: Promise<{ operationId: number }>) => p.then((r) => { setConfirm(null); watch(r) }).catch((e) => toast(e.message, 'error'))
  const kernel = u && u.kernelInstalled && u.kernelInstalled !== u.kernelRunning ? `${u.kernelRunning} → ${u.kernelInstalled}` : u?.kernelRunning || lh.capacity.kernel
  const downtime = clusters.length ? <p>Every VM stops for a few minutes; {clusters.map((c) => <span class="mono" key={c}>{c}</span>)} {clusters.length === 1 ? 'is' : 'are'} NotReady meanwhile and come back when the host is up.</p> : <p>The {running} running VM{running === 1 ? '' : 's'} stop for a few minutes and start again with the host.</p>
  return (
    <div class="panel">
      <div class="flex items-center gap-3 px-4 py-2.5 border-b border-border">
        <span class="font-medium">System</span>
        {u && (u.count > 0 || reboot) ? <Pill tone={reboot ? 'warn' : 'info'}>{u.count > 0 ? `${u.count} update${u.count === 1 ? '' : 's'}` : ''}{u.count > 0 && reboot ? ' · ' : ''}{reboot ? 'reboot required' : ''}</Pill> : u ? <Pill tone="good">up to date</Pill> : null}
        {u && <span class="text-[12px] text-muted">checked {fmt.when(u.checkedAt)}</span>}
        <span class="ml-auto flex gap-2">
          <button class="btn !py-1" disabled={checking || busy} onClick={check}>{checking ? 'Checking' : 'Check now'}</button>
          <button class="btn !py-1" disabled={busy} onClick={() => setConfirm('reboot')}>Reboot host</button>
          <button class="btn btn-primary !py-1" disabled={busy} onClick={() => setConfirm('update')}>Update host</button>
        </span>
      </div>
      <dl class="grid grid-cols-2 lg:grid-cols-4 gap-x-6 gap-y-2 px-4 py-3 text-[13px]">
        <Fact label="OS" value={u?.release || 'Debian'} />
        <Fact label="Kernel" value={kernel} mono />
        <Fact label="Updates" value={u ? `${u.count} pending${u.security ? `, ${u.security} security` : ''}` : 'not checked yet'} />
        <Fact label="Security updates" value={u ? (u.unattended ? 'automatic, daily' : 'manual') : '—'} />
        <Fact label="Host" value={`${lh.capacity.hostname} · ${host.ip}`} mono />
        <Fact label="Install disk" value={lh.disk || 'largest at install'} mono />
        <Fact label="libvirt" value={lh.capacity.libvirt || '—'} mono />
        <Fact label="Bridge" value={lh.capacity.bridge} mono />
        <Fact label="Talos boot assets" value={lh.talos || '—'} mono />
      </dl>
      {confirm === 'update' && (
        <ConfirmDialog title={`Update ${lh.capacity.hostname}`} action="Update host" onClose={() => setConfirm(null)} onConfirm={() => start(api.labUpdate(host.mac))} impact={
          <>
            <p>Installs {u ? `${u.count} package update${u.count === 1 ? '' : 's'}` : 'pending package updates'} with apt.</p>
            {reboot ? <><p class="text-warn">A reboot is required.</p>{downtime}</> : <p>No reboot expected; VMs keep running. A new kernel would reboot the host and stop the VMs for a few minutes.</p>}
            {clusters.map((c) => <MaintenanceNotice key={c} cluster={c} />)}
          </>
        } />
      )}
      {confirm === 'reboot' && (
        <ConfirmDialog title={`Reboot ${lh.capacity.hostname}`} action="Reboot host" tone="danger" onClose={() => setConfirm(null)} onConfirm={() => start(api.labReboot(host.mac))} impact={<>{downtime}{clusters.map((c) => <MaintenanceNotice key={c} cluster={c} />)}</>} />
      )}
    </div>
  )
}

export function MacSystem({ host, lh }: { host: NodeRow; lh: LabHost }) {
  return (
    <div class="panel">
      <div class="flex items-center gap-3 px-4 py-2.5 border-b border-border"><span class="font-medium">System</span></div>
      <dl class="grid grid-cols-2 lg:grid-cols-4 gap-x-6 gap-y-2 px-4 py-3 text-[13px]">
        <Fact label="OS" value={lh.capacity.os || 'macOS'} />
        <Fact label="Model" value={lh.capacity.model || '—'} mono />
        <Fact label="Hypervisor" value={lh.capacity.hypervisor || '—'} mono />
        <Fact label="Kept for macOS" value={fmt.bytes(mib(reserveOf(lh)))} />
        <Fact label="Host" value={`${lh.capacity.hostname} · ${host.ip}`} mono />
        <Fact label="VM network" value={`vmnet ${host.ip.replace(/\.\d+$/, '.0/24')}`} mono />
        <Fact label="Talos ISO" value={lh.talos || '—'} mono />
      </dl>
    </div>
  )
}

function Fact({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return <div class="flex flex-col min-w-0"><dt class="label">{label}</dt><dd class={`truncate ${mono ? 'mono text-[12px]' : ''}`} title={value}>{value}</dd></div>
}

/** Make lab host: install the host, and optionally carve VMs and create a cluster in one run. */
function usePlan(memMiB: number, reserve: number, cluster = 'lab', most = Infinity) {
  const [withVMs, setWithVMs] = useState(true)
  const [withCluster, setWithCluster] = useState(true)
  const [rows, setRows] = useState<VMRow[]>(() => planFor(memMiB, true, reserve, most))
  const [name, setName] = useState(cluster)
  const [repo, setRepo] = useState({ url: '', path: '' })
  const cps = cpCount(rows)
  const need = withVMs ? totalMem(rows) : 0
  const over = memMiB > 0 && withVMs && need > memMiB - reserve
  const nameOk = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(name)
  const topologyOk = cps === 1 || cps === 3
  const vmProblem = withVMs ? rowsProblem(withCluster ? rows : rows.map((v) => ({ ...v, role: 'worker' as const }))) : null
  const blocked = over || !!vmProblem || (withVMs && withCluster && (!nameOk || !topologyOk))
  const body = withVMs ? { vms: { each: rows.map(({ key: _k, ...v }) => (withCluster ? v : { ...v, role: 'worker' as const })) }, cluster: withCluster ? { name, controlPlanes: cps as 1 | 3, repository: repo.url.trim() ? { url: repo.url.trim(), path: repo.path.trim() || undefined } : undefined } : undefined } : {}
  return { withVMs, setWithVMs, withCluster, setWithCluster, rows, setRows, name, setName, repo, setRepo, cps, need, over, vmProblem, topologyOk, blocked, body, memMiB, reserve }
}

function PlanFields({ p, keeps }: { p: ReturnType<typeof usePlan>; keeps: string }) {
  const avail = p.memMiB - p.reserve
  return (
    <>
      <label class="flex items-center gap-2 text-[13px] font-medium"><input type="checkbox" checked={p.withVMs} onChange={(e) => p.setWithVMs((e.target as HTMLInputElement).checked)} /> Add Talos VMs</label>
      {p.withVMs && (
        <div class="flex flex-col gap-3">
          <VMTable rows={p.rows} onChange={p.setRows} roles={p.withCluster} />
          {p.memMiB > 0 ? <Meter label={`Memory: ${fmt.bytes(mib(p.need))} of ${fmt.bytes(mib(avail))} (${keeps} keeps ${fmt.bytes(mib(p.reserve))})`} used={p.need} cap={Math.max(1, avail)} format={(n) => fmt.bytes(mib(n))} /> : <span class="text-[12px] text-muted">{fmt.bytes(mib(p.need))} of memory for VMs; checked against the host after the install.</span>}
          {p.over && <Notice tone="bad">Not enough memory.</Notice>}
          {!p.over && p.vmProblem && <Notice tone="bad">{p.vmProblem}.</Notice>}
        </div>
      )}
      {p.withVMs && <label class="flex items-center gap-2 text-[13px] font-medium"><input type="checkbox" checked={p.withCluster} onChange={(e) => p.setWithCluster((e.target as HTMLInputElement).checked)} /> Create a cluster from them</label>}
      {p.withVMs && p.withCluster && (
        <div class="grid grid-cols-2 gap-3 items-end">
          <Field label="Cluster name"><input class="input mono" value={p.name} onInput={(e) => p.setName((e.target as HTMLInputElement).value.toLowerCase())} /></Field>
          <div class="text-[13px] pb-2">{p.topologyOk ? <span>{p.cps} control plane{p.cps === 1 ? '' : 's'}, {p.rows.length - p.cps} worker{p.rows.length - p.cps === 1 ? '' : 's'}</span> : <span class="text-bad">Choose 1 or 3 control planes.</span>}</div>
          <Field label="Apps repository" hint="Public HTTPS Git URL Flux syncs; optional."><input class="input mono" value={p.repo.url} placeholder="https://github.com/you/apps.git" onInput={(e) => p.setRepo({ ...p.repo, url: (e.target as HTMLInputElement).value })} /></Field>
          <Field label="Path"><input class="input mono" value={p.repo.path} placeholder="./" onInput={(e) => p.setRepo({ ...p.repo, path: (e.target as HTMLInputElement).value })} /></Field>
        </div>
      )}
    </>
  )
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
      <p class="text-[13px]"><span class="text-bad">The install disk is wiped.</span> Debian + KVM installs unattended (≈10 min){manual ? '; you boot the installer with the line shown on the Lab host tab' : ' after a reset by remote management'}.</p>
      <Field label="Install disk" hint={disks.length > 1 ? 'Wiped; Debian and the VM images live here.' : disks.length === 1 ? 'The only disk; wiped.' : 'Disks are unknown until the machine has booted Talos once; the installer takes the largest.'}>
        {disks.length > 1
          ? <select class="input mono" value={disk} onChange={(e) => setDisk((e.target as HTMLSelectElement).value)}>
              {disks.map((d) => <option key={d.devPath} value={d.devPath}>{d.devPath} · {fmt.bytes(d.sizeBytes)}{d.model ? ` · ${d.model}` : ''}{d.transport ? ` · ${d.transport}` : ''}</option>)}
            </select>
          : <div class="input mono text-muted">{disks[0] ? `${disks[0].devPath} · ${fmt.bytes(disks[0].sizeBytes)}${disks[0].model ? ` · ${disks[0].model}` : ''}` : 'largest disk'}</div>}
      </Field>
      <PlanFields p={p} keeps="host" />
    </Dialog>
  )
}

export function ThisMacDialog({ onClose }: { onClose: () => void }) {
  const [info, setInfo] = useState<LabLocal | null>(null)
  const [error, setError] = useState<string | null>(null)
  const load = () => { setError(null); api.labLocal().then(setInfo).catch((e) => setError(e.message)) }
  useEffect(load, [])
  if (info?.supported && !info.problem && !info.host && info.capacity) return <ThisMacPlan info={info} onClose={onClose} />
  return (
    <Dialog title="Lab host on this Mac" onClose={onClose} footer={<><button class="btn" onClick={onClose}>Close</button>{info?.supported && info.problem && <button class="btn btn-primary" onClick={load}>Check again</button>}</>}>
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
      <p class="text-[13px]">Talos VMs run on this Mac under vfkit, on {info.subnet}.</p>
      <PlanFields p={p} keeps="this Mac" />
    </Dialog>
  )
}
