import { useState } from 'preact/hooks'
import { api, fmt, type LabHost, type NodeRow } from '../../api'
import { labNeedsReboot, vmsOf } from '../../machine'
import { subnet24 } from '../../net'
import { runOp } from '../../ops'
import { machines, toast } from '../../store'
import { ConfirmDialog, MaintenanceNotice, Pill } from '../ui'
import { mib, reserveOf } from './plan'

export function HostSystem({ host, lh, busy }: { host: NodeRow; lh: LabHost; busy: boolean }) {
  const vms = vmsOf(lh)
  const u = lh.updates
  const reboot = labNeedsReboot(u)
  const [confirm, setConfirm] = useState<'update' | 'reboot' | null>(null)
  const [checking, setChecking] = useState(false)
  const clusters = [...new Set(vms.map((vm) => machines.value.get(vm.mac)?.cluster).filter((c): c is string => !!c))]
  const running = vms.filter((v) => v.state === 'running').length
  const check = () => { setChecking(true); api.labCheck(host.mac).catch((e) => toast(e.message, 'error')).finally(() => setChecking(false)) }
  const start = (p: Promise<{ operationId: number }>) => runOp(p).then((ok) => { if (ok) setConfirm(null) })
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
          <button class="btn btn-sm" disabled={checking || busy} onClick={check}>{checking ? 'Checking' : 'Check now'}</button>
          <button class="btn btn-sm" disabled={busy} onClick={() => setConfirm('reboot')}>Reboot host</button>
          <button class="btn btn-primary btn-sm" disabled={busy} onClick={() => setConfirm('update')}>Update host</button>
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
