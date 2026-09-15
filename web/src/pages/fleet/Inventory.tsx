import { useEffect, useState } from 'preact/hooks'
import { api, fmt, type NodeRow } from '../../api'
import { useLocation } from 'preact-iso'
import { clusters, toast, watch } from '../../store'
import { DataTable, type Column } from '../../components/DataTable'
import { ConfirmDialog, ErrorBox, Pill, Section, stateTone } from '../../components/ui'
import { TypePill } from '../create/steps'
import { operations } from '../../store'

/** Every machine Kubit knows: maintenance-mode candidates and cluster members. */
export function Inventory() {
  const [nodes, setNodes] = useState<NodeRow[]>([])
  const [loaded, setLoaded] = useState(false)
  const [targets, setTargets] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [retire, setRetire] = useState<NodeRow | null>(null)
  const { route } = useLocation()
  const load = () => api.nodes().then((ns) => { setNodes(ns); if (!targets && ns[0]) setTargets((t) => t || ns[0].ip.replace(/\.\d+$/, '.0/24')) }).catch((e) => setError(e.message)).finally(() => setLoaded(true))
  useEffect(() => { load(); api.settings().then((v) => { if (v.discoverySubnets.length) setTargets(v.discoverySubnets.join(', ')) }).catch(() => {}) }, []) // eslint-disable-line
  const adopt = (n: NodeRow) => {
    const ready = clusters.value.filter((c) => c.state === 'ready' || c.state === 'bootstrapped')
    if (ready.length === 0) { route('/clusters/new'); return }
    const target = ready.length === 1 ? ready[0].name : prompt(`Adopt ${n.ip} into which cluster? (${ready.map((c) => c.name).join(', ')})`, ready[0].name)
    if (target && ready.find((c) => c.name === target)) route(`/clusters/${target}/nodes?adopt=${n.ip}`)
  }
  const finishedDiscoveries = [...operations.value.values()].filter((o) => o.kind === 'discover' && o.status !== 'running').length
  useEffect(() => { load() }, [finishedDiscoveries]) // eslint-disable-line
  const scanning = [...operations.value.values()].some((o) => o.kind === 'discover' && o.status === 'running')

  const columns: Column<NodeRow>[] = [
    { id: 'mac', header: 'Machine', sort: (n) => n.mac, mono: true, text: (n) => `${n.mac} ${n.uuid ?? ''} ${n.serial ?? ''}`, cell: (n) => <a href={`/machines/${n.mac}`} class="hover:underline flex flex-col"><span>{n.mac}</span>{(n.uuid || n.serial) && <span class="text-[10px] text-muted truncate max-w-[220px]">{n.serial || n.uuid}</span>}</a> },
    { id: 'ip', header: 'IP', sort: (n) => n.ip, mono: true, text: (n) => `${n.ip} ${(n.ipsSeen ?? []).join(' ')}`, cell: (n) => <span class="flex flex-col"><span>{n.ip}</span>{(n.ipsSeen?.length ?? 0) > 1 && <span class="text-[10px] text-muted" title={`Addresses seen: ${n.ipsSeen!.join(', ')}`}>previously {n.ipsSeen!.filter((x) => x !== n.ip).join(', ')}</span>}</span> },
    { id: 'state', header: 'State', sort: (n) => n.state, cell: (n) => <Pill tone={stateTone(n.state)}>{n.state}</Pill> },
    { id: 'cluster', header: 'Cluster / pool', sort: (n) => n.cluster, cell: (n) => n.cluster ? <span><a href={`/clusters/${n.cluster}/nodes`} class="text-accent hover:underline">{n.cluster}</a>{n.pool && <span class="text-muted"> / {n.pool}</span>}</span> : <span class="text-muted">unassigned</span> },
    { id: 'hostname', header: 'Hostname', sort: (n) => n.hostname, cell: (n) => n.hostname || <span class="text-muted">—</span> },
    { id: 'arch', header: 'Arch', sort: (n) => n.arch, cell: (n) => n.arch },
    { id: 'model', header: 'Model', sort: (n) => `${n.inventory?.manufacturer ?? ''} ${n.inventory?.product ?? ''}`, cell: (n) => <span class="flex items-center gap-2"><span class="text-muted truncate max-w-[180px]">{[n.inventory?.manufacturer, n.inventory?.product].filter(Boolean).join(' ') || '—'}</span>{n.inventory && <TypePill m={n} />}</span> },
    { id: 'talos', header: 'Talos', sort: (n) => n.talosVersion, mono: true, cell: (n) => n.talosVersion },
    { id: 'cpu', header: 'CPU', align: 'right', sort: (n) => n.inventory?.cpus ?? 0, cell: (n) => n.inventory?.cpus ?? '—' },
    { id: 'ram', header: 'RAM', align: 'right', sort: (n) => n.inventory?.memoryBytes ?? 0, cell: (n) => n.inventory ? fmt.bytes(n.inventory.memoryBytes) : '—' },
    { id: 'disks', header: 'Disks', mono: true, text: (n) => (n.inventory?.disks ?? []).map((d) => d.devPath).join(' '), cell: (n) => (n.inventory?.disks.filter((d) => !d.readonly && !d.cdrom && d.transport !== 'usb').map((d) => `${d.devPath} ${fmt.bytes(d.sizeBytes)}`).join(', ')) || '—' },
    { id: 'kvm', header: 'KVM', sort: (n) => n.inventory?.kvm ? 1 : 0, cell: (n) => n.inventory?.kvm ? 'yes' : 'no' },
    { id: 'seen', header: 'Last seen', sort: (n) => n.lastSeen, cell: (n) => <span class="num text-muted">{fmt.when(n.lastSeen)}</span> },
    { id: 'actions', header: '', align: 'right', cell: (n) => (
      <span class="whitespace-nowrap flex gap-1 justify-end">
        {n.wol && <button class="btn !py-1" title="Send a Wake-on-LAN magic packet" onClick={() => api.wake(n.mac).then(() => toast('Magic packet sent', 'good')).catch((e) => toast(e.message, 'error'))}>Wake</button>}
        {n.state === 'maintenance' && !n.cluster && <button class="btn btn-primary !py-1" onClick={() => adopt(n)}>Adopt…</button>}
        {!n.cluster && <button class="btn !py-1" title="Forget this machine" onClick={() => setRetire(n)}>Retire</button>}
        <a href={`/machines/${n.mac}`} class="btn !py-1">Open</a>
      </span>
    ) },
  ]

  return (
    <div class="p-6 flex flex-col gap-5">
      <Section title="Inventory" help="Every machine Kubit has seen, identified by its uplink MAC (plus SMBIOS UUID and serial when reported). Machines booted from a Talos ISO or over PXE wait in maintenance mode on port 50000; scanning records their hardware so they can be adopted. A machine that moves to a new DHCP address keeps its row."
        actions={<><a class="btn" href="/start" title="ISO downloads and the three steps to a cluster">Getting started</a><a class="btn" href="/fleet/pxe" title="Boot machines over the network (proxyDHCP + iPXE)">Network boot</a></>}>
        <ErrorBox error={error} />
        <div class="panel p-4 flex gap-2">
          <input class="input mono" value={targets} onInput={(e) => setTargets((e.target as HTMLInputElement).value)} placeholder="192.168.1.0/24, 10.0.0.5" aria-label="Subnets or addresses to scan" />
          <button class="btn btn-primary shrink-0" disabled={scanning} onClick={() => api.discover(targets.split(/[,\s]+/).filter(Boolean)).then((r) => watch(r)).catch((e) => toast(e.message, 'error'))}>{scanning ? 'Scanning…' : 'Scan'}</button>
        </div>
        <DataTable loading={!loaded} id="inventory" columns={columns} rows={nodes} rowKey={(n) => n.mac || n.ip} defaultSort={{ id: 'ip', dir: 'asc' }} empty="No machines known yet. Scan a subnet." />
      </Section>
      {retire && <ConfirmDialog title={`Retire ${retire.hostname || retire.mac}`} action="Retire" tone="danger" onClose={() => setRetire(null)}
        onConfirm={() => api.retireMachine(retire.mac).then(() => { setRetire(null); load() }).catch((e) => toast(e.message, 'error'))}
        impact={<p>Deletes the inventory row for <span class="mono">{retire.mac}</span> (hardware record, address history). Nothing is sent to the machine; it reappears on the next scan if still online.</p>} />}
    </div>
  )
}
