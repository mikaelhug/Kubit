import type { ComponentChildren } from 'preact'
import { useEffect, useState } from 'preact/hooks'
import { api, fmt, labHostKey, labNeedsReboot, vmsOf, type LabHost, type LabVM, type NodeRow, type Sample } from '../api'
import { ack, health, hostSamples, loadHealth, machineList, operations, toast, watch } from '../store'
import { ConfirmDialog, Dialog, ErrorBox, Field, MaintenanceNotice, Meter, Notice, Pill } from './ui'
import { usePxeGated } from './PxeGate'
import { Sparkline } from './Sparkline'
import { EventRow } from '../pages/cluster/Overview'

const RESERVED_MIB = 2048
// MiB to bytes without the 32-bit `<<` that wraps at 2 GiB.
const mib = (n: number) => n * 1048576

/** Sizes VMs against what the host has left; the suggestion is one control plane plus workers. */
export function AddVMsDialog({ host, onClose }: { host: NodeRow; onClose: () => void }) {
  const lh = host.labhost!
  const vms = vmsOf(lh)
  const used = vms.reduce((s, v) => s + v.memMiB, 0)
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
      <Meter label={`Memory: ${fmt.bytes(mib(need))} of ${fmt.bytes(mib(freeMiB))} free (host keeps 2 GiB)`} used={need} cap={Math.max(freeMiB, 1)} format={(n) => fmt.bytes(mib(n))} />
      {over && <Notice tone="bad">Not enough memory: reduce the count or the RAM per VM.</Notice>}
      {overCpu && <Notice tone="warn">More than 2× the host's CPUs; the VMs will contend.</Notice>}
      {count >= 4 && <Notice tone="muted">Suggestion for {count}: 1 control plane + {count - 1} workers (no HA — the host is one failure domain anyway), or 3 control planes + {count - 3} workers to rehearse HA.</Notice>}
      <p class="text-[12px] text-muted">The VMs boot Talos into maintenance mode straight from the host and appear as machines within a minute or two. Nothing is installed on their disks until you create or adopt a cluster.</p>
    </Dialog>
  )
}

/** The Lab host tab on a machine page: alerts, host utilisation, system and updates, VMs. */
export function LabHostPanel({ host }: { host: NodeRow }) {
  const lh = host.labhost
  const [add, setAdd] = useState(false)
  const [release, setRelease] = useState(false)
  const [del, setDel] = useState<LabVM | null>(null)
  if (!lh) return null
  const vms = vmsOf(lh)
  const rows = machineList.value
  const rowOf = (vm: LabVM) => rows.find((m) => m.mac === vm.mac)
  const busy = [...operations.value.values()].some((o) => o.status === 'running' && (o.request as { host?: string } | undefined)?.host === host.mac)
  const run = (p: Promise<{ operationId: number }>) => p.then((r) => watch(r, false)).catch((e) => toast(e.message, 'error'))
  return (
    <div class="flex flex-col gap-4">
      {lh.state === 'error' && <Notice tone="bad">Lab host setup failed: {lh.error}</Notice>}
      {lh.state === 'installing' && <Notice tone="warn">Debian is being installed unattended; this takes about ten minutes. Progress is in the Activity drawer.</Notice>}
      {lh.state === 'updating' && <Notice tone="warn">Host maintenance running; progress is in the Activity drawer.</Notice>}
      <HostAlerts mac={host.mac} />
      <HostMetrics host={host} lh={lh} />
      <HostSystem host={host} lh={lh} busy={busy || lh.state !== 'ready'} />
      <div class="panel">
        <div class="flex items-center gap-3 px-4 py-2.5 border-b border-border">
          <span class="font-medium">Virtual machines</span>
          <span class="ml-auto flex gap-2">
            <button class="btn btn-primary !py-1" disabled={lh.state !== 'ready'} onClick={() => setAdd(true)}>+ Add VMs</button>
            <button class="btn btn-danger !py-1" onClick={() => setRelease(true)}>Release host</button>
          </span>
        </div>
        <table class="data wrap">
          <thead><tr><th class="pl-4">VM</th><th>State</th><th>Size</th><th>Boot</th><th>Kubit</th><th></th></tr></thead>
          <tbody>
            {vms.length === 0 && <tr><td colSpan={6} class="pl-4 py-3 text-muted">No VMs yet.</td></tr>}
            {vms.map((vm) => {
              const row = rowOf(vm)
              return (
                <tr key={vm.name}>
                  <td class="pl-4"><span class="font-medium mono">{vm.name}</span><span class="block text-[11px] text-muted mono">{vm.mac}{vm.ip ? ` · ${vm.ip}` : ''}</span></td>
                  <td><Pill tone={vm.state === 'running' ? 'good' : 'muted'}>{vm.state}</Pill></td>
                  <td class="num">{vm.cpus} vCPU · {fmt.bytes(mib(vm.memMiB))} · {vm.diskGiB} GiB</td>
                  <td><Pill tone={vm.boot === 'disk' ? 'info' : 'muted'}>{vm.boot === 'disk' ? 'disk' : 'Talos (RAM)'}</Pill></td>
                  <td>{row ? (row.cluster ? <a class="text-accent hover:underline" href={`/clusters/${row.cluster}/nodes`}>{row.cluster} · {row.hostname}</a> : <Pill tone={row.state === 'maintenance' ? 'good' : 'warn'}>{row.state}</Pill>) : <span class="text-muted">—</span>}</td>
                  <td class="text-right pr-3 whitespace-nowrap">
                    {vm.state === 'running' ? <button class="btn !py-1" onClick={() => run(api.labVM(host.mac, vm.name, 'stop'))}>Stop</button> : <button class="btn !py-1" onClick={() => run(api.labVM(host.mac, vm.name, 'start'))}>Start</button>}
                    {' '}<button class="btn !py-1" disabled={!!row?.cluster} title={row?.cluster ? 'Remove it from the cluster first' : 'Boot back into Talos maintenance mode'} onClick={() => run(api.labVM(host.mac, vm.name, 'reprovision'))}>Re-provision</button>
                    {' '}<button class="btn btn-danger !py-1" disabled={!!row?.cluster} onClick={() => setDel(vm)}>Delete</button>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
      {add && <AddVMsDialog host={host} onClose={() => setAdd(false)} />}
      {del && <ConfirmDialog title={`Delete ${del.name}`} action="Delete VM" tone="danger" onClose={() => setDel(null)} onConfirm={() => api.labVMDelete(host.mac, del.name).then(() => setDel(null)).catch((e) => toast(e.message, 'error'))} impact={<p>Destroys the VM and its disk.</p>} />}
      {release && <ConfirmDialog title={`Release ${lh.capacity.hostname || host.hostname}`} action="Release host" tone="danger" typed={host.hostname || 'release'} onClose={() => setRelease(false)} onConfirm={() => api.labRelease(host.mac).then(() => setRelease(false)).catch((e) => toast(e.message, 'error'))} impact={<p>Deletes every VM and drops the lab-host role. Debian stays on the disk.</p>} />}
    </div>
  )
}

/** Open alerts for the host, from the same watcher path clusters use. */
function HostAlerts({ mac }: { mac: string }) {
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
function HostMetrics({ host, lh }: { host: NodeRow; lh: LabHost }) {
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
  const pts = (f: (s: Sample) => number) => samples.filter((s) => s.reachable).map((s) => ({ t: new Date(s.ts).getTime(), v: f(s) }))
  const committed = vms.reduce((s, v) => s + v.diskGiB, 0)
  const diskPct = m && m.diskTotal ? fmt.pct(m.diskUsed, m.diskTotal) : 0
  const memPct = m && m.memTotal ? fmt.pct(m.memUsed, m.memTotal) : 0
  const tone = (pct: number, warn: number, bad: number) => (pct >= bad ? 'text-bad' : pct >= warn ? 'text-warn' : '')
  return (
    <div class="panel p-4 flex flex-col gap-3">
      <div class="flex items-center gap-2">
        <span class="label">Host utilisation</span>
        {m && <span class="text-[12px] text-muted">load {m.load1.toFixed(2)} · up {fmt.uptime(m.uptimeSec)} · read {fmt.when(m.at)}</span>}
        <div class="ml-auto flex gap-1">
          {['1h', '6h', '24h', '7d'].map((r) => <button key={r} class={`btn !py-0.5 !px-2 text-[11px] ${r === range ? 'border-accent text-accent' : ''}`} onClick={() => setRange(r)}>{r}</button>)}
        </div>
      </div>
      <div class="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-4 gap-4">
        <MetricCard label="CPU" value={m ? `${Math.round(m.cpuPct)}%` : '—'} sub={`${lh.capacity.cpus} cores · ${vms.reduce((s, v) => s + v.cpus, 0)} vCPU assigned`} cls={m ? tone(m.cpuPct, 80, 95) : ''}>
          <Sparkline label="" points={pts((s) => s.cpuMilli / 10)} max={100} format={(v) => `${Math.round(v)}%`} height={44} />
        </MetricCard>
        <MetricCard label="Memory" value={m ? fmt.bytes(m.memUsed) : '—'} sub={m ? `of ${fmt.bytes(m.memTotal)} · ${fmt.bytes(mib(vms.reduce((s, v) => s + v.memMiB, 0)))} assigned to VMs` : ''} cls={tone(memPct, 85, 92)}>
          <Sparkline label="" points={pts((s) => s.memBytes)} max={m?.memTotal} format={fmt.bytes} height={44} />
        </MetricCard>
        <MetricCard label="VM disk" value={m ? `${diskPct}%` : '—'} sub={m ? `${fmt.bytes(m.diskUsed)} of ${fmt.bytes(m.diskTotal)} · VMs may grow to ${committed} GiB` : ''} cls={tone(diskPct, 85, 95)}>
          <Sparkline label="" points={pts((s) => s.disk ?? 0)} max={m?.diskTotal} format={fmt.bytes} height={44} />
        </MetricCard>
        <MetricCard label="VMs running" value={m ? `${m.vmsRunning}/${vms.length}` : `${vms.filter((v) => v.state === 'running').length}/${vms.length}`} sub="defined on this host">
          <Sparkline label="" points={pts((s) => s.pods)} max={Math.max(1, vms.length)} format={String} height={44} />
        </MetricCard>
      </div>
      <span class="text-[11px] text-muted">Read over SSH once a minute; kept 24 h, then hourly for 30 d. Gaps mean the host did not answer.</span>
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
function HostSystem({ host, lh, busy }: { host: NodeRow; lh: LabHost; busy: boolean }) {
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
        <Fact label="libvirt" value={lh.capacity.libvirt || '—'} mono />
        <Fact label="Bridge" value={lh.capacity.bridge} mono />
        <Fact label="Talos boot assets" value={lh.talos || '—'} mono />
      </dl>
      {confirm === 'update' && (
        <ConfirmDialog title={`Update ${lh.capacity.hostname}`} action="Update host" onClose={() => setConfirm(null)} onConfirm={() => start(api.labUpdate(host.mac))} impact={
          <>
            <p>Installs {u ? `${u.count} package update${u.count === 1 ? '' : 's'}` : 'pending package updates'} with apt.</p>
            {reboot ? <><p class="text-warn">A reboot is required.</p>{downtime}</> : <p>No reboot expected; VMs keep running. If the upgrade brings a new kernel, the host reboots afterwards and the VMs stop for a few minutes.</p>}
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

function Fact({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return <div class="flex flex-col min-w-0"><dt class="label">{label}</dt><dd class={`truncate ${mono ? 'mono text-[12px]' : ''}`} title={value}>{value}</dd></div>
}

/** Wizard: lab hosts and the machines you can add there. */
export function LabHostsSection() {
  const hosts = machineList.value.filter((m) => m.labhost)
  const [add, setAdd] = useState<NodeRow | null>(null)
  if (hosts.length === 0) return null
  return (
    <div class="panel">
      <div class="flex items-center gap-3 px-4 py-2.5 border-b border-border"><span class="font-medium">Lab hosts</span></div>
      <table class="data wrap">
        <thead><tr><th class="pl-4">Host</th><th>State</th><th>Capacity</th><th>VMs</th><th></th></tr></thead>
        <tbody>
          {hosts.map((h) => {
            const lh = h.labhost!
            const vms = vmsOf(lh)
            const used = vms.reduce((s, v) => s + v.memMiB, 0)
            return (
              <tr key={h.mac}>
                <td class="pl-4"><a class="font-medium hover:underline" href={`/machines/${h.mac}#labhost`}>{lh.capacity.hostname || h.hostname || h.mac}</a><span class="block text-[11px] text-muted mono">{h.ip} · {h.mac}</span></td>
                <td><Pill tone={lh.state === 'ready' ? 'good' : lh.state === 'error' ? 'bad' : 'warn'}>{lh.state}</Pill></td>
                <td class="num">{lh.capacity.cpus} CPU · {fmt.bytes(mib(lh.capacity.memMiB))} · {lh.metrics?.diskTotal ? `disk ${fmt.pct(lh.metrics.diskUsed, lh.metrics.diskTotal)}% used, ${fmt.bytes(lh.metrics.diskTotal - lh.metrics.diskUsed)} free` : `${lh.capacity.diskGiB} GiB free`}</td>
                <td class="num">{vms.length} ({fmt.bytes(mib(used))})</td>
                <td class="text-right pr-3"><button class="btn btn-primary !py-1" disabled={lh.state !== 'ready'} onClick={() => setAdd(h)}>+ Add VMs</button></td>
              </tr>
            )
          })}
        </tbody>
      </table>
      {add && <AddVMsDialog host={add} onClose={() => setAdd(null)} />}
    </div>
  )
}

/** Make lab host: install the host, and optionally carve VMs and create a cluster in one run. */
export function MakeLabHostDialog({ m, onClose }: { m: NodeRow; onClose: () => void }) {
  const memMiB = Math.floor((m.inventory?.memoryBytes ?? 0) / (1 << 20))
  const known = memMiB > 0
  const [withVMs, setWithVMs] = useState(true)
  const [count, setCount] = useState(4)
  const [cpus, setCpus] = useState(2)
  const [mem, setMem] = useState(3072)
  const [disk, setDisk] = useState(20)
  const [withCluster, setWithCluster] = useState(true)
  const [name, setName] = useState('lab')
  const [cps, setCps] = useState<1 | 3>(1)
  const [error, setError] = useState<string | null>(null)
  const plan = withVMs ? { vms: { count, cpus, memMiB: mem, diskGiB: disk }, cluster: withCluster ? { name, controlPlanes: cps } : undefined } : {}
  const gated = usePxeGated(() => api.labProvision(m.mac, plan), (r) => { onClose(); watch(r) }, (msg) => setError(msg))
  if (gated.element) return gated.element
  const over = known && withVMs && count * mem > memMiB - RESERVED_MIB
  const nameOk = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(name)
  return (
    <Dialog title={`Make ${m.hostname || m.ip} a lab host`} onClose={onClose} footer={<><button class="btn" onClick={onClose}>Cancel</button><button class="btn btn-primary" disabled={over || (withVMs && withCluster && (!nameOk || cps > count))} onClick={() => gated.attempt('Make lab host')}>{withVMs && withCluster ? 'Install and create cluster' : withVMs ? 'Install and add VMs' : 'Install'}</button></>}>
      <ErrorBox error={error} />
      <ul class="list-disc pl-5 text-[13px] flex flex-col gap-1">
        <li class="text-bad">The disk is wiped.</li>
        <li>Debian + KVM installs unattended over the network (≈10 min).</li>
      </ul>
      <label class="flex items-center gap-2 text-[13px] font-medium"><input type="checkbox" checked={withVMs} onChange={(e) => setWithVMs((e.target as HTMLInputElement).checked)} /> Then add Talos VMs</label>
      {withVMs && (
        <div class="grid grid-cols-4 gap-3 pl-6">
          <Field label="Count"><input class="input num" type="number" min={1} max={32} value={count} onInput={(e) => setCount(Number((e.target as HTMLInputElement).value))} /></Field>
          <Field label="vCPUs"><input class="input num" type="number" min={1} value={cpus} onInput={(e) => setCpus(Number((e.target as HTMLInputElement).value))} /></Field>
          <Field label="RAM (MiB)"><input class="input num" type="number" min={1024} step={256} value={mem} onInput={(e) => setMem(Number((e.target as HTMLInputElement).value))} /></Field>
          <Field label="Disk (GiB)"><input class="input num" type="number" min={8} value={disk} onInput={(e) => setDisk(Number((e.target as HTMLInputElement).value))} /></Field>
          {known && <div class="col-span-4"><Meter label={`Memory: ${fmt.bytes(mib(count * mem))} of ${fmt.bytes(mib(memMiB - RESERVED_MIB))}`} used={count * mem} cap={Math.max(1, memMiB - RESERVED_MIB)} format={(n) => fmt.bytes(mib(n))} /></div>}
          {over && <Notice tone="bad">Not enough memory.</Notice>}
        </div>
      )}
      {withVMs && <label class="flex items-center gap-2 text-[13px] font-medium"><input type="checkbox" checked={withCluster} onChange={(e) => setWithCluster((e.target as HTMLInputElement).checked)} /> Then create a cluster from them</label>}
      {withVMs && withCluster && (
        <div class="grid grid-cols-2 gap-3 pl-6">
          <Field label="Cluster name"><input class="input mono" value={name} onInput={(e) => setName((e.target as HTMLInputElement).value.toLowerCase())} /></Field>
          <Field label="Topology">
            <select class="input" value={cps} onChange={(e) => setCps(Number((e.target as HTMLSelectElement).value) as 1 | 3)}>
              <option value={1}>1 control plane, {Math.max(0, count - 1)} workers</option>
              <option value={3} disabled={count < 3}>3 control planes, {Math.max(0, count - 3)} workers</option>
            </select>
          </Field>
        </div>
      )}
      <p class="text-[12px] text-muted">Runs unattended; progress in Activity.</p>
    </Dialog>
  )
}
