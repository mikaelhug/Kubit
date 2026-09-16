import { useEffect, useState } from 'preact/hooks'
import { api, fmt, labHostKey, type NodeRow } from '../../api'
import { useLocation } from 'preact-iso'
import { clusters, connected, health, loadHealth, machineList, operations, resyncing, settings, toast, watch } from '../../store'
import { DataTable, type Column } from '../../components/DataTable'
import { AlertPill, ConfirmDialog, ErrorBox, Pill, Section, stateTone } from '../../components/ui'
import { TypePill } from '../create/steps'
import { AddAMTDialog } from '../../components/RemoteManagement'

/** Every machine Kubit knows: maintenance-mode candidates and cluster members. */
export function Inventory() {
  const nodes = machineList.value
  const loaded = connected.value && !resyncing.value
  const [targets, setTargetsRaw] = useState('')
  const [typed, setTyped] = useState(false)
  const setTargets = (v: string) => { setTyped(true); setTargetsRaw(v) }
  const error = null
  const [retire, setRetire] = useState<NodeRow | null>(null)
  const [addAMT, setAddAMT] = useState(false)
  const { route } = useLocation()
  const subnets = settings.value?.discoverySubnets ?? []
  useEffect(() => { if (!typed) setTargetsRaw(subnets.length ? subnets.join(', ') : nodes[0] ? nodes[0].ip.replace(/\.\d+$/, '.0/24') : '') }, [subnets.join(','), nodes.length]) // eslint-disable-line
  const adopt = (n: NodeRow) => {
    const ready = clusters.value.filter((c) => c.state === 'ready' || c.state === 'bootstrapped')
    if (ready.length === 0) { route('/clusters/new'); return }
    const target = ready.length === 1 ? ready[0].name : prompt(`Adopt ${n.ip} into which cluster? (${ready.map((c) => c.name).join(', ')})`, ready[0].name)
    if (target && ready.find((c) => c.name === target)) route(`/clusters/${target}/nodes?adopt=${n.ip}`)
  }
  const scanning = [...operations.value.values()].some((o) => o.kind === 'discover' && o.status === 'running')

  const labHosts = machineList.value.filter((m) => m.labhost).map((m) => m.mac).join(',')
  useEffect(() => { for (const mac of labHosts.split(',').filter(Boolean)) loadHealth(labHostKey(mac)) }, [labHosts])
  const hostAlert = (mac: string) => (health.value.get(labHostKey(mac)) ?? []).find((e) => !e.acked && e.severity !== 'info')
  const columns: Column<NodeRow>[] = [
    { id: 'mac', header: 'Machine', sort: (n) => n.mac, mono: true, text: (n) => `${n.mac} ${n.uuid ?? ''} ${n.serial ?? ''}`, cell: (n) => <a href={`/machines/${n.mac}`} class="hover:underline flex flex-col"><span>{n.mac}</span>{(n.uuid || n.serial) && <span class="text-[10px] text-muted truncate max-w-[220px]">{n.serial || n.uuid}</span>}</a> },
    { id: 'ip', header: 'IP', sort: (n) => n.ip, mono: true, text: (n) => `${n.ip} ${(n.ipsSeen ?? []).join(' ')}`, cell: (n) => { const earlier = (n.ipsSeen ?? []).filter((x) => x !== n.ip); return <span class="flex items-center gap-1.5 whitespace-nowrap"><span>{n.ip}</span>{earlier.length > 0 && <Pill tone="muted" title={`Earlier addresses: ${earlier.join(', ')}`}>+{earlier.length}</Pill>}</span> } },
    { id: 'state', header: 'State', sort: (n) => n.state, cell: (n) => <span class="flex items-center gap-1"><Pill tone={stateTone(n.state)}>{n.state}</Pill>{n.oobType && <Pill tone="info" title="Remote management (Intel AMT) configured">AMT</Pill>}{n.labhost && <AlertPill e={hostAlert(n.mac)} />}</span> },
    { id: 'cluster', header: 'Cluster / pool', sort: (n) => n.cluster, cell: (n) => n.cluster ? <span><a href={`/clusters/${n.cluster}/nodes`} class="text-accent hover:underline">{n.cluster}</a>{n.pool && <span class="text-muted"> / {n.pool}</span>}</span> : <span class="text-muted">unassigned</span> },
    { id: 'hostname', header: 'Hostname', sort: (n) => n.hostname, cell: (n) => n.hostname || <span class="text-muted">—</span> },
    { id: 'model', header: 'Model', sort: (n) => `${n.inventory?.manufacturer ?? ''} ${n.inventory?.product ?? ''}`, text: (n) => `${n.inventory?.manufacturer ?? ''} ${n.inventory?.product ?? ''} ${n.arch}`, cell: (n) => <span class="flex flex-col min-w-0"><span class="flex items-center gap-1.5"><span class="text-muted truncate max-w-[160px]" title={[n.inventory?.manufacturer, n.inventory?.product].filter(Boolean).join(' ')}>{[n.inventory?.manufacturer, n.inventory?.product].filter(Boolean).join(' ') || '—'}</span>{n.inventory && <TypePill m={n} />}</span><span class="text-[10px] text-muted mono">{n.arch}{n.inventory?.kvm ? ' · kvm' : ''}{n.talosVersion ? ` · ${n.talosVersion}` : ''}</span></span> },
    { id: 'resources', header: 'CPU · RAM · Disk', align: 'right', sort: (n) => n.inventory?.memoryBytes ?? 0, text: (n) => (n.inventory?.disks ?? []).map((d) => d.devPath).join(' '), cell: (n) => { const disks = (n.inventory?.disks ?? []).filter((d) => !d.readonly && !d.cdrom && d.transport !== 'usb'); return n.inventory ? <span class="num whitespace-nowrap" title={disks.map((d) => `${d.devPath} ${fmt.bytes(d.sizeBytes)}`).join(', ')}>{n.inventory.cpus} · {fmt.bytes(n.inventory.memoryBytes)} · {disks.length ? `${fmt.bytes(disks[0].sizeBytes)}${disks.length > 1 ? ` +${disks.length - 1}` : ''}` : '—'}</span> : <span class="text-muted">—</span> } },
    { id: 'seen', header: 'Last seen', sort: (n) => n.lastSeen, cell: (n) => <span class="num text-muted">{fmt.when(n.lastSeen)}</span> },
    { id: 'actions', header: '', align: 'right', cell: (n) => (
      <span class="whitespace-nowrap flex gap-1 justify-end">
        {n.wol && <button class="btn !py-1" title="Send a Wake-on-LAN magic packet" onClick={() => api.wake(n.mac).then(() => toast('Magic packet sent', 'good')).catch((e) => toast(e.message, 'error'))}>Wake</button>}
        {n.state === 'maintenance' && !n.cluster && <button class="btn btn-primary !py-1" onClick={() => adopt(n)}>Adopt</button>}
        {!n.cluster && <button class="btn !py-1" title="Forget this machine" onClick={() => setRetire(n)}>Retire</button>}
        <a href={`/machines/${n.mac}`} class="btn !py-1">Open</a>
      </span>
    ) },
  ]

  return (
    <div class="p-6 flex flex-col gap-5">
      <Section title="Inventory" help="Every machine Kubit has seen, by MAC. Scan to find machines in Talos maintenance mode."
        actions={<><a class="btn" href="/start" title="ISO downloads and the three steps to a cluster">Getting started</a><button class="btn btn-primary" onClick={() => setAddAMT(true)} title="Register a machine by its Intel AMT address; it can then be powered on and booted into Talos from here">+ Add via AMT</button></>}>
        <ErrorBox error={error} />
        <div class="panel p-4 flex gap-2">
          <input class="input mono" value={targets} onInput={(e) => setTargets((e.target as HTMLInputElement).value)} placeholder="192.168.1.0/24, 10.0.0.5" aria-label="Subnets or addresses to scan" />
          <button class="btn btn-primary shrink-0" disabled={scanning} onClick={() => api.discover(targets.split(/[,\s]+/).filter(Boolean)).then((r) => watch(r)).catch((e) => toast(e.message, 'error'))}>{scanning ? 'Scanning…' : 'Scan'}</button>
        </div>
        <DataTable loading={!loaded} id="inventory" columns={columns} rows={nodes} rowKey={(n) => n.mac || n.ip} defaultSort={{ id: 'ip', dir: 'asc' }} empty="No machines known yet. Scan a subnet." />
      </Section>
      {addAMT && <AddAMTDialog onClose={() => setAddAMT(false)} />}
      {retire && <ConfirmDialog title={`Retire ${retire.hostname || retire.mac}`} action="Retire" tone="danger" onClose={() => setRetire(null)}
        onConfirm={() => api.retireMachine(retire.mac).then(() => setRetire(null)).catch((e) => toast(e.message, 'error'))}
        impact={<p>Deletes the inventory row for <span class="mono">{retire.mac}</span> (hardware record, address history). Nothing is sent to the machine; it reappears on the next scan if still online.</p>} />}
    </div>
  )
}
