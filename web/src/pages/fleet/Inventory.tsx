import { useEffect, useMemo, useState } from 'preact/hooks'
import { useLocation } from 'preact-iso'
import { api, fmt, type NodeRow } from '../../api'
import { DataTable, type Column } from '../../components/DataTable'
import { MakeLabHostDialog, ThisMacDialog } from '../../components/labhost'
import { PxeGate } from '../../components/PxeGate'
import { AddAMTDialog } from '../../components/RemoteManagement'
import { ScanBox } from '../../components/ScanBox'
import { AlertPill, Pill, Section } from '../../components/ui'
import { adopt, bootTalosBlocked, canAdopt, canMakeLabHost, canRetire, groupLabel, groupOf, hostName, hostOf, KindPill, labHostKey, lastSeenOf, modelOf, onMac, provisionLabel, RetireDialog, typeOf, vmsOf, wake, type MachineGroup } from '../../machine'
import { connected, daemon, labHosts, loadHealth, machineList, openAlerts, resyncing, settings, toast, versions, watch } from '../../store'
import { defaultTalos, talosIso } from '../../versions'

const groupOrder: Record<MachineGroup, number> = { available: 0, boot: 1, 'in-use': 2 }
const filters: (MachineGroup | 'all')[] = ['all', 'available', 'boot', 'in-use']
const openHref = (n: NodeRow) => (n.kind === 'labhost' ? `/labhosts/${n.mac}/overview` : `/machines/${n.mac}`)
const canBoot = (m: NodeRow) => !!m.oobType && groupOf(m) === 'boot' && !bootTalosBlocked(m)

export function Inventory() {
  const { route, query } = useLocation()
  const all = machineList.value
  const loaded = connected.value && !resyncing.value
  const filter = (filters.includes(query.filter as MachineGroup) ? query.filter : 'all') as MachineGroup | 'all'
  const setFilter = (f: MachineGroup | 'all') => route(f === 'all' ? '/fleet/inventory' : `/fleet/inventory?filter=${f}`, true)
  const [showVMs, setShowVMs] = useState(false)
  const physical = useMemo(() => all.filter((m) => !m.host), [all])
  const counts: Record<MachineGroup, number> = { available: 0, boot: 0, 'in-use': 0 }
  for (const m of physical) counts[groupOf(m)]++
  const rows = useMemo(() => (showVMs ? all : physical).filter((m) => filter === 'all' || groupOf(m) === filter), [all, physical, showVMs, filter])
  const [retire, setRetire] = useState<NodeRow | null>(null)
  const [addAMT, setAddAMT] = useState(false)
  const [lab, setLab] = useState<NodeRow | null>(null)
  const [mac, setMac] = useState(false)
  const macFree = daemon.value?.os === 'darwin' && !all.some((m) => onMac(m.labhost))
  const [gate, setGate] = useState<string[] | null>(null)
  const [busy, setBusy] = useState<Record<string, boolean>>({})
  const factory = settings.value?.factoryUrl ?? 'https://factory.talos.dev'
  const talos = defaultTalos(versions.value)
  const hostKeys = labHosts.value.map((m) => m.mac).join(',')
  useEffect(() => { for (const m of hostKeys.split(',').filter(Boolean)) loadHealth(labHostKey(m)) }, [hostKeys])

  const boot = (macs: string[]) => {
    setBusy((b) => ({ ...b, ...Object.fromEntries(macs.map((m) => [m, true])) }))
    Promise.all(macs.map((m) => api.power(m, 'pxe').then((r) => watch(r, false))))
      .catch((e) => { if (e?.code === 'pxe-down') setGate(macs); else toast(e.message, 'error') })
      .finally(() => setBusy((b) => ({ ...b, ...Object.fromEntries(macs.map((m) => [m, false])) })))
  }
  const bootable = rows.filter(canBoot)

  const columns = useMemo<Column<NodeRow>[]>(() => [
    { id: 'machine', header: 'Machine', sort: (n) => `${groupOrder[groupOf(n)]} ${n.ip}`, text: (n) => `${modelOf(n)} ${n.mac} ${n.uuid ?? ''} ${n.serial ?? ''} ${n.hostname}`, cell: (n) => (
      <a href={openHref(n)} class="flex flex-col min-w-0 hover:underline">
        <span class="font-medium truncate">{n.hostname || n.labhost?.capacity.hostname || modelOf(n)}<span class="text-[10px] text-muted font-normal ml-1.5">{typeOf(n)}</span></span>
        <span class="text-[10px] text-muted mono truncate">{[n.hostname || n.labhost?.capacity.hostname ? modelOf(n).replace('Unknown hardware', '') : '', n.serial, n.mac].filter(Boolean).join(' · ')}</span>
      </a>
    ) },
    { id: 'ip', header: 'Address', sort: (n) => n.ip, mono: true, text: (n) => `${n.ip} ${(n.ipsSeen ?? []).join(' ')}`, cell: (n) => { const earlier = (n.ipsSeen ?? []).filter((x) => x !== n.ip); return <span class="flex items-center gap-1.5 whitespace-nowrap"><span>{n.ip || '—'}</span>{earlier.length > 0 && <Pill tone="muted" title={`Earlier addresses: ${earlier.join(', ')}`}>+{earlier.length}</Pill>}</span> } },
    { id: 'state', header: 'State', sort: (n) => `${groupOrder[groupOf(n)]} ${n.kind} ${n.state}`, text: (n) => `${n.state} ${n.kind} ${groupLabel[groupOf(n)]}`, cell: (n) => { const alert = n.labhost ? openAlerts(labHostKey(n.mac))[0] : undefined; return <span class="flex items-center gap-1 flex-wrap"><KindPill m={n} />{n.provision && <Pill tone="warn" title={n.provisionKind === 'labhost' ? 'Next network boot gets the Debian installer' : 'Next network boot gets Talos'}>{provisionLabel(n)}</Pill>}{alert?.kind !== 'labhost.unreachable' && <AlertPill e={alert} />}</span> } },
    { id: 'remote', header: 'Remote', sort: (n) => n.oobType ?? '', cell: (n) => n.oobType ? <Pill tone="info" title={n.oobType === 'redfish' ? 'Redfish BMC configured' : 'Intel AMT configured'}>{n.oobType === 'redfish' ? 'BMC' : 'AMT'}</Pill> : <span class="text-muted">—</span> },
    { id: 'runs', header: 'Runs', sort: (n) => n.cluster || (n.labhost ? 'lab host' : n.host ? `on ${hostName(hostOf(n))}` : ''), cell: (n) => {
      if (n.cluster) return <span><a href={`/clusters/${n.cluster}/nodes`} class="text-accent hover:underline">{n.cluster}</a>{n.pool && <span class="text-muted"> / {n.pool}</span>}</span>
      if (n.labhost) { const c = vmsOf(n.labhost).length; return <a href={`/labhosts/${n.mac}/vms`} class="text-accent hover:underline">lab host · {c} VM{c === 1 ? '' : 's'}</a> }
      const host = hostOf(n)
      if (host) return <a href={`/labhosts/${host.mac}/vms`} class="text-accent hover:underline">on {hostName(host)}</a>
      return <span class="text-muted">—</span>
    } },
    { id: 'resources', header: 'CPU · RAM · Disk', align: 'right', sort: (n) => n.inventory?.memoryBytes ?? 0, text: (n) => (n.inventory?.disks ?? []).map((d) => d.devPath).join(' '), cell: (n) => { const disks = (n.inventory?.disks ?? []).filter((d) => !d.readonly && !d.cdrom && d.transport !== 'usb'); return n.inventory ? <span class="whitespace-nowrap" title={disks.map((d) => `${d.devPath} ${fmt.bytes(d.sizeBytes)}`).join(', ')}>{n.inventory.cpus} · {fmt.bytes(n.inventory.memoryBytes)} · {disks.length ? `${fmt.bytes(disks[0].sizeBytes)}${disks.length > 1 ? ` +${disks.length - 1}` : ''}` : '—'}<span class="block text-[10px] text-muted">{n.arch}{n.inventory.kvm ? ' · kvm' : ''}</span></span> : <span class="text-muted">—</span> } },
    { id: 'seen', header: 'Last seen', sort: (n) => lastSeenOf(n), cell: (n) => <span class="text-muted">{fmt.when(lastSeenOf(n))}</span> },
    { id: 'actions', header: '', align: 'right', cell: (n) => (
      <span class="whitespace-nowrap flex gap-1 justify-end">
        {canAdopt(n) && <button class="btn btn-primary !py-1" onClick={() => adopt(n, route)}>Adopt</button>}
        {canBoot(n) && <button class="btn btn-primary !py-1" disabled={busy[n.mac]} title={n.provision ? 'Armed; click to boot again' : 'One network boot into Talos maintenance mode'} onClick={() => boot([n.mac])}>{busy[n.mac] ? 'Starting' : 'Boot into Talos'}</button>}
        {canMakeLabHost(n) && n.kind !== 'maintenance' && <button class="btn !py-1" onClick={() => setLab(n)}>Make lab host</button>}
        {n.wol && !n.host && <button class="btn !py-1" title="Send a Wake-on-LAN magic packet" onClick={() => wake(n.mac)}>Wake</button>}
        {!n.cluster && canRetire(n) && <button class="btn !py-1" title="Forget this machine" onClick={() => setRetire(n)}>Retire</button>}
        <a href={openHref(n)} class="btn !py-1">Open</a>
      </span>
    ) },
  ], [busy, route])

  return (
    <div class="p-5 flex flex-col gap-4">
      <Section title="Inventory" help="Every physical machine Kubit has seen, by MAC."
        actions={<span class="flex gap-2">{macFree && <button class="btn" onClick={() => setMac(true)} title="Run Talos VMs on this Mac">+ Lab host on this Mac</button>}<button class="btn btn-primary" onClick={() => setAddAMT(true)} title="Register a machine by its Intel AMT or BMC address">+ Add by remote management</button></span>}>
        <div class="panel p-3 flex flex-col gap-2">
          <ScanBox primary openDrawer fallbackIp={physical[0]?.ip} onError={(m) => toast(m, 'error')} />
          <div class="text-[12px] text-muted flex flex-wrap gap-x-3">
            <span>Finds Talos maintenance mode, Intel AMT and Redfish BMCs.</span>
            <span>Boot from USB: <a class="text-accent hover:underline" href={talosIso(factory, talos, 'amd64')}>Talos ISO amd64</a> · <a class="text-accent hover:underline" href={talosIso(factory, talos, 'arm64')}>arm64</a></span>
            <a class="text-accent hover:underline" href="/fleet/network-boot">Boot over the network</a>
          </div>
        </div>
        <div class="flex items-center gap-1 flex-wrap">
          {filters.map((f) => <button key={f} class={`btn !py-1 ${filter === f ? 'border-accent text-accent' : ''}`} onClick={() => setFilter(f)}>{f === 'all' ? 'All' : groupLabel[f]} <span class="text-muted">{f === 'all' ? physical.length : counts[f]}</span></button>)}
          <label class="ml-2 flex items-center gap-1.5 text-[12px] text-muted"><input type="checkbox" checked={showVMs} onChange={(e) => setShowVMs((e.target as HTMLInputElement).checked)} /> Show lab VMs</label>
          {bootable.length > 1 && <button class="btn btn-primary !py-1 ml-auto" onClick={() => boot(bootable.map((m) => m.mac))}>Boot all {bootable.length} into Talos</button>}
        </div>
        <DataTable loading={!loaded} id="inventory" columns={columns} rows={rows} rowKey={(n) => n.mac || n.ip} defaultSort={{ id: 'machine', dir: 'asc' }} empty={filter === 'all' ? 'No machines known yet. Scan a subnet or add one by remote management.' : `No machines in ${groupLabel[filter].toLowerCase()}.`} />
      </Section>
      {addAMT && <AddAMTDialog onClose={() => setAddAMT(false)} />}
      {lab && <MakeLabHostDialog m={lab} onClose={() => setLab(null)} />}
      {mac && <ThisMacDialog onClose={() => setMac(false)} />}
      {gate && <PxeGate what="Boot into Talos" onClose={() => setGate(null)} onReady={() => { const macs = gate; setGate(null); boot(macs) }} />}
      {retire && <RetireDialog m={retire} onClose={() => setRetire(null)} onDone={() => setRetire(null)} />}
    </div>
  )
}
