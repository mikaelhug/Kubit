import { useEffect, useMemo, useState } from 'preact/hooks'
import { api, fmt, type LabHost, type LabVM, type NodeRow, type Sample } from '../../api'
import { hostName, hostOf, KindPill, labHostKey, labNeedsReboot, labOffline, onMac, vmsOf } from '../../machine'
import { subnet24 } from '../../net'
import { hostSamples, loadHealth, machines, openAlerts, toast, watch } from '../../store'
import { levelTone } from '../../tone'
import { useLive } from '../../useLive'
import { AlertGroup } from '../Alerts'
import { DataTable, type Column } from '../DataTable'
import { Sparkline } from '../Sparkline'
import { Code, ConfirmDialog, MaintenanceNotice, Notice, Pill, Tile } from '../ui'
import { AddVMsDialog, MakeLabHostDialog, ResizeVMDialog } from './dialogs'
import { mib, reserveOf, totalMem } from './plan'

const runOp = (p: Promise<{ operationId: number }>) => p.then((r) => watch(r, false)).catch((e) => toast(e.message, 'error'))

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
        {vm.state === 'running' ? <button class="btn !py-1" disabled={member || offline} title={member ? 'Drain and remove it from the cluster first' : ''} onClick={() => runOp(api.labVM(host.mac, vm.name, 'stop'))}>Stop</button> : <button class="btn !py-1" disabled={offline} onClick={() => runOp(api.labVM(host.mac, vm.name, 'start'))}>Start</button>}
        <button class="btn !py-1" disabled={offline} onClick={() => setResize(vm)}>Resize</button>
        <button class="btn !py-1" disabled={member || offline} title={member ? 'Remove it from the cluster first' : 'Boot back into Talos maintenance mode'} onClick={() => runOp(api.labVM(host.mac, vm.name, 'reprovision'))}>Re-provision</button>
        <button class="btn btn-danger !py-1" disabled={member || offline} onClick={() => setDel(vm)}>Delete</button>
      </span>
    ) } },
  ], [byMac, offline, lh, host.mac])
  if (!lh) return null
  const vms = vmsOf(lh)
  return (
    <div class="flex flex-col gap-4">
      <DataTable search={false} columns={columns} rows={vms} rowKey={(vm) => vm.name} empty="No VMs yet."
        title={<><span class="font-medium">Virtual machines</span><span class="text-[12px] text-muted">{fmt.bytes(mib(totalMem(vms)))} of {fmt.bytes(mib(Math.max(0, lh.capacity.memMiB - reserveOf(lh))))} assigned</span></>}
        toolbar={<button class="btn btn-primary !py-1" disabled={lh.state !== 'ready' || offline} onClick={() => setAdd(true)}>+ Add VMs</button>} />
      {add && <AddVMsDialog host={host} onClose={() => setAdd(false)} />}
      {resize && <ResizeVMDialog host={host} vm={resize} onClose={() => setResize(null)} />}
      {del && <ConfirmDialog title={`Delete ${del.name}`} action="Delete VM" tone="danger" onClose={() => setDel(null)} onConfirm={() => api.labVMDelete(host.mac, del.name).then(() => setDel(null)).catch((e) => toast(e.message, 'error'))} impact={<p>Destroys the VM and its disk.</p>} />}
    </div>
  )
}

const installStages: Record<string, string> = { installer: 'installer started', partitioning: 'partitioning', packages: 'packages installed', 'late-done': 'rebooting', booted: 'booted into Debian' }

export function HostStateNotice({ host }: { host: NodeRow }) {
  const lh = host.labhost
  const [retry, setRetry] = useState(false)
  if (!lh) return null
  return (
    <>
      {lh.state === 'error' && <Notice tone="bad"><span class="flex items-center gap-3">Lab host setup failed: {lh.error}{!onMac(lh) && <button class="btn !py-1 ml-auto shrink-0" onClick={() => setRetry(true)}>Make lab host</button>}</span></Notice>}
      {retry && <MakeLabHostDialog m={host} onClose={() => setRetry(false)} />}
      {lh.state === 'installing' && <Notice tone="warn">Installing Debian{lh.install ? <> · {installStages[lh.install.stage] ?? lh.install.stage} · {fmt.when(lh.install.at)}</> : ' · waiting for the installer to boot'}</Notice>}
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
      {lh.state === 'setup' && <Notice tone="warn">{onMac(lh) ? 'Checking vfkit and fetching the Talos ISO' : 'Verifying KVM and fetching Talos boot assets'}</Notice>}
      {lh.state === 'updating' && <Notice tone="warn">Host maintenance running</Notice>}
    </>
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
        {entry.state === 'running' ? <button class="btn" disabled={member} title={member ? 'Drain and remove it from the cluster first' : ''} onClick={() => runOp(api.labVM(host.mac, entry.name, 'stop'))}>Stop</button> : <button class="btn btn-primary" onClick={() => runOp(api.labVM(host.mac, entry.name, 'start'))}>Start</button>}
        <button class="btn" disabled={member} title={member ? 'Remove it from the cluster first' : 'Boot back into Talos maintenance mode'} onClick={() => runOp(api.labVM(host.mac, entry.name, 'reprovision'))}>Re-provision</button>
        <button class="btn btn-danger" disabled={member} onClick={() => setDel(true)}>Delete</button>
      </div>
      {del && <ConfirmDialog title={`Delete ${entry.name}`} action="Delete VM" tone="danger" onClose={() => setDel(false)} onConfirm={() => api.labVMDelete(host.mac, entry.name).then(() => setDel(false)).catch((e) => toast(e.message, 'error'))} impact={<p>Destroys the VM and its disk.</p>} />}
    </div>
  )
}

export function HostAlerts({ mac }: { mac: string }) {
  const key = labHostKey(mac)
  useEffect(() => { loadHealth(key) }, [key])
  return <AlertGroup id={key} alerts={openAlerts(key)} />
}

const ranges = ['1h', '6h', '24h', '7d']

export function HostMetrics({ host, lh }: { host: NodeRow; lh: LabHost }) {
  const vms = vmsOf(lh)
  const [range, setRange] = useState('24h')
  const { data: samples, set } = useLive(() => api.labSamples(host.mac, range), [host.mac, range], [], { onError: 'silent' })
  const live = hostSamples.value.get(host.mac)
  useEffect(() => {
    if (live) set((prev) => (prev?.length && prev[prev.length - 1].ts >= live.ts ? prev : [...(prev ?? []), live]))
  }, [live?.ts])
  const series = useMemo(() => {
    const pts = (f: (s: Sample) => number) => (samples ?? []).map((s) => ({ t: Date.parse(s.ts), v: s.reachable ? f(s) : null }))
    return { cpu: pts((s) => s.cpuMilli / 10), mem: pts((s) => s.memBytes), disk: pts((s) => s.disk ?? 0), vms: pts((s) => s.pods) }
  }, [samples])
  const m = lh.metrics
  const committed = vms.reduce((s, v) => s + v.diskGiB, 0)
  const diskPct = m && m.diskTotal ? fmt.pct(m.diskUsed, m.diskTotal) : 0
  const memPct = m && m.memTotal ? fmt.pct(m.memUsed, m.memTotal) : 0
  const offline = labOffline(lh)
  const graph = offline ? 'bad' : 'accent'
  const tone = (t?: ReturnType<typeof levelTone>) => (offline ? 'muted' : t)
  return (
    <div class="panel p-3 flex flex-col gap-3">
      <div class="flex items-center gap-2">
        <span class="label">Host utilisation</span>
        {m && !offline && <span class="text-[12px] text-muted">load {m.load1.toFixed(2)} · up {fmt.span(m.uptimeSec * 1000)}</span>}
        <div class="ml-auto flex gap-1">
          {ranges.map((r) => <button key={r} class={`btn btn-xs ${r === range ? 'border-accent text-accent' : ''}`} onClick={() => setRange(r)}>{r}</button>)}
        </div>
      </div>
      <div class="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-4 gap-4">
        <Tile plain label="CPU" value={m ? `${Math.round(m.cpuPct)}%` : '—'} sub={`${lh.capacity.cpus} cores · ${vms.reduce((s, v) => s + v.cpus, 0)} vCPU assigned`} tone={tone(m ? levelTone(m.cpuPct, 80, 95) : undefined)}>
          <Sparkline label="" points={series.cpu} max={100} format={(v) => `${Math.round(v)}%`} height={44} tone={graph} />
        </Tile>
        <Tile plain label="Memory" value={m ? fmt.bytes(m.memUsed) : '—'} sub={m ? `of ${fmt.bytes(m.memTotal)} · ${fmt.bytes(mib(totalMem(vms)))} assigned to VMs` : ''} tone={tone(levelTone(memPct, 85, 92))}>
          <Sparkline label="" points={series.mem} max={m?.memTotal} format={fmt.bytes} height={44} tone={graph} />
        </Tile>
        <Tile plain label="VM disk" value={m ? `${diskPct}%` : '—'} sub={m ? `${fmt.bytes(m.diskUsed)} of ${fmt.bytes(m.diskTotal)} · VMs may grow to ${committed} GiB` : ''} tone={tone(levelTone(diskPct, 85, 95))}>
          <Sparkline label="" points={series.disk} max={m?.diskTotal} format={fmt.bytes} height={44} tone={graph} />
        </Tile>
        <Tile plain label="VMs running" value={offline ? '—' : m ? `${m.vmsRunning}/${vms.length}` : `${vms.filter((v) => v.state === 'running').length}/${vms.length}`} sub={`${vms.length} defined on this host`} tone={tone()}>
          <Sparkline label="" points={series.vms} max={Math.max(1, vms.length)} format={String} height={44} tone={graph} />
        </Tile>
      </div>
      <span class="text-[11px] text-muted">Read every minute; gaps mean the host did not answer.</span>
    </div>
  )
}

export function HostSystem({ host, lh, busy }: { host: NodeRow; lh: LabHost; busy: boolean }) {
  const vms = vmsOf(lh)
  const u = lh.updates
  const reboot = labNeedsReboot(u)
  const [confirm, setConfirm] = useState<'update' | 'reboot' | null>(null)
  const [checking, setChecking] = useState(false)
  const clusters = [...new Set(vms.map((vm) => machines.value.get(vm.mac)?.cluster).filter((c): c is string => !!c))]
  const running = vms.filter((v) => v.state === 'running').length
  const check = () => { setChecking(true); api.labCheck(host.mac).catch((e) => toast(e.message, 'error')).finally(() => setChecking(false)) }
  const start = (p: Promise<{ operationId: number }>) => p.then((r) => { setConfirm(null); watch(r) }).catch((e) => toast(e.message, 'error'))
  const kernel = u && u.kernelInstalled && u.kernelInstalled !== u.kernelRunning ? `${u.kernelRunning} → ${u.kernelInstalled}` : u?.kernelRunning || lh.capacity.kernel
  const downtime = clusters.length ? <p>Every VM stops for a few minutes; {clusters.map((c) => <span class="mono" key={c}>{c} </span>)}{clusters.length === 1 ? 'is' : 'are'} NotReady meanwhile.</p> : <p>The {running} running VM{running === 1 ? '' : 's'} stop for a few minutes.</p>
  const notices = clusters.map((c) => <MaintenanceNotice key={c} cluster={c} />)
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
            {reboot ? <><p class="text-warn">A reboot is required.</p>{downtime}</> : <p>No reboot expected; VMs keep running.</p>}
            {notices}
          </>
        } />
      )}
      {confirm === 'reboot' && (
        <ConfirmDialog title={`Reboot ${lh.capacity.hostname}`} action="Reboot host" tone="danger" onClose={() => setConfirm(null)} onConfirm={() => start(api.labReboot(host.mac))} impact={<>{downtime}{notices}</>} />
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
        <Fact label="VM network" value={`vmnet ${subnet24(host.ip)}`} mono />
        <Fact label="Talos ISO" value={lh.talos || '—'} mono />
      </dl>
    </div>
  )
}

function Fact({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return <div class="flex flex-col min-w-0"><dt class="label">{label}</dt><dd class={`truncate ${mono ? 'mono text-[12px]' : ''}`} title={value}>{value}</dd></div>
}
