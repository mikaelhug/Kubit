import { useEffect, useState } from 'preact/hooks'
import { api, fmt, labHostKey, type NodeRow, type Versions } from '../../api'
import { useLocation } from 'preact-iso'
import { clusters, connected, health, latestTalos, loadHealth, machineList, operations, resyncing, settings, toast, watch } from '../../store'
import { DataTable, type Column } from '../../components/DataTable'
import { AlertPill, ConfirmDialog, Pill, Section } from '../../components/ui'
import { bootTalosBlocked, canAdopt, canMakeLabHost, canRetire, formOf, groupLabel, groupOf, hostName, hostOf, KindPill, modelOf, provisionLabel, type MachineGroup } from '../../machine'
import { AddAMTDialog } from '../../components/RemoteManagement'
import { MakeLabHostDialog } from '../../components/LabHost'
import { PxeGate } from '../../components/PxeGate'

const vanillaSchematic = '376567988ad370138ad8b2698212367b8edcb69b5fd68c80be1f2ec7d603b4ba'
const groupOrder: Record<MachineGroup, number> = { available: 0, boot: 1, 'in-use': 2 }
const filters: (MachineGroup | 'all')[] = ['all', 'available', 'boot', 'in-use']

/** The hardware ledger: every physical machine Kubit has seen, what it does now, what it can do next. */
export function Inventory() {
  const { route, query } = useLocation()
  const all = machineList.value
  const loaded = connected.value && !resyncing.value
  const filter = (filters.includes(query.filter as MachineGroup) ? query.filter : 'all') as MachineGroup | 'all'
  const setFilter = (f: MachineGroup | 'all') => route(f === 'all' ? '/fleet/inventory' : `/fleet/inventory?filter=${f}`, true)
  const [showVMs, setShowVMs] = useState(false)
  const physical = all.filter((m) => !m.host)
  const counts: Record<MachineGroup, number> = { available: 0, boot: 0, 'in-use': 0 }
  for (const m of physical) counts[groupOf(m)]++
  const rows = (showVMs ? all : physical).filter((m) => filter === 'all' || groupOf(m) === filter)
  const [targets, setTargetsRaw] = useState('')
  const [typed, setTyped] = useState(false)
  const setTargets = (v: string) => { setTyped(true); setTargetsRaw(v) }
  const [retire, setRetire] = useState<NodeRow | null>(null)
  const [addAMT, setAddAMT] = useState(false)
  const [lab, setLab] = useState<NodeRow | null>(null)
  const [gate, setGate] = useState<string[] | null>(null)
  const [busy, setBusy] = useState<Record<string, boolean>>({})
  const subnets = settings.value?.discoverySubnets ?? []
  useEffect(() => { if (!typed) setTargetsRaw(subnets.length ? subnets.join(', ') : physical[0] ? physical[0].ip.replace(/\.\d+$/, '.0/24') : '') }, [subnets.join(','), physical.length]) // eslint-disable-line
  const scanning = [...operations.value.values()].some((o) => o.kind === 'discover' && o.status === 'running')
  const [versions, setVersions] = useState<Versions | null>(null)
  useEffect(() => { api.versions().then(setVersions).catch(() => {}) }, [latestTalos.value])
  const factory = settings.value?.factoryUrl ?? 'https://factory.talos.dev'
  const talos = versions?.talos.find((v) => !v.includes('-')) ?? versions?.minTalos ?? 'v1.14.0'
  const iso = (arch: 'amd64' | 'arm64') => `${factory}/image/${vanillaSchematic}/${talos}/metal-${arch}.iso`

  const labHosts = all.filter((m) => m.labhost).map((m) => m.mac).join(',')
  useEffect(() => { for (const mac of labHosts.split(',').filter(Boolean)) loadHealth(labHostKey(mac)) }, [labHosts])
  const hostAlert = (mac: string) => (health.value.get(labHostKey(mac)) ?? []).find((e) => !e.acked && e.severity !== 'info')

  const adopt = (n: NodeRow) => {
    const ready = clusters.value.filter((c) => c.state === 'ready' || c.state === 'bootstrapped')
    if (ready.length === 0) { route('/clusters/new'); return }
    const target = ready.length === 1 ? ready[0].name : prompt(`Adopt ${n.ip} into which cluster? (${ready.map((c) => c.name).join(', ')})`, ready[0].name)
    if (target && ready.find((c) => c.name === target)) route(`/clusters/${target}/nodes?adopt=${n.ip}`)
  }
  const canBoot = (m: NodeRow) => !!m.oobType && groupOf(m) === 'boot' && !bootTalosBlocked(m)
  const boot = (macs: string[]) => {
    setBusy((b) => { const n = { ...b }; macs.forEach((m) => { n[m] = true }); return n })
    Promise.all(macs.map((mac) => api.power(mac, 'pxe').then((r) => watch(r, false))))
      .catch((e) => { if (e?.code === 'pxe-down') setGate(macs); else toast(e.message, 'error') })
      .finally(() => setBusy((b) => { const n = { ...b }; macs.forEach((m) => { n[m] = false }); return n }))
  }
  const bootable = rows.filter(canBoot)
  const openHref = (n: NodeRow) => (n.kind === 'labhost' ? `/labhosts/${n.mac}/overview` : `/machines/${n.mac}`)

  const columns: Column<NodeRow>[] = [
    { id: 'machine', header: 'Machine', sort: (n) => `${groupOrder[groupOf(n)]} ${n.ip}`, text: (n) => `${modelOf(n)} ${n.mac} ${n.uuid ?? ''} ${n.serial ?? ''} ${n.hostname}`, cell: (n) => (
      <a href={openHref(n)} class="flex flex-col min-w-0 hover:underline">
        <span class="font-medium truncate">{n.hostname || n.labhost?.capacity.hostname || modelOf(n)}<span class="text-[10px] text-muted font-normal ml-1.5">{formOf(n)}</span></span>
        <span class="text-[10px] text-muted mono truncate">{[n.hostname || n.labhost?.capacity.hostname ? modelOf(n).replace('Unknown hardware', '') : '', n.serial, n.mac].filter(Boolean).join(' · ')}</span>
      </a>
    ) },
    { id: 'ip', header: 'Address', sort: (n) => n.ip, mono: true, text: (n) => `${n.ip} ${(n.ipsSeen ?? []).join(' ')}`, cell: (n) => { const earlier = (n.ipsSeen ?? []).filter((x) => x !== n.ip); return <span class="flex items-center gap-1.5 whitespace-nowrap"><span>{n.ip || '—'}</span>{earlier.length > 0 && <Pill tone="muted" title={`Earlier addresses: ${earlier.join(', ')}`}>+{earlier.length}</Pill>}</span> } },
    { id: 'state', header: 'State', sort: (n) => `${groupOrder[groupOf(n)]} ${n.kind} ${n.state}`, text: (n) => `${n.state} ${n.kind} ${groupLabel[groupOf(n)]}`, cell: (n) => <span class="flex items-center gap-1 flex-wrap"><KindPill m={n} />{n.provision && <Pill tone="warn" title={n.provisionKind === 'labhost' ? 'Armed: next network boot gets the Debian installer' : 'Armed: next network boot gets Talos'}>{provisionLabel(n)}</Pill>}{n.labhost && <AlertPill e={hostAlert(n.mac)} />}</span> },
    { id: 'remote', header: 'Remote', sort: (n) => n.oobType ?? '', cell: (n) => n.oobType ? <Pill tone="info" title={n.oobType === 'redfish' ? 'Redfish BMC configured' : 'Intel AMT configured'}>{n.oobType === 'redfish' ? 'BMC' : 'AMT'}</Pill> : <span class="text-muted">—</span> },
    { id: 'runs', header: 'Runs', sort: (n) => n.cluster || (n.labhost ? 'lab host' : n.host ? `on ${hostName(hostOf(n))}` : ''), cell: (n) => {
      if (n.cluster) return <span><a href={`/clusters/${n.cluster}/nodes`} class="text-accent hover:underline">{n.cluster}</a>{n.pool && <span class="text-muted"> / {n.pool}</span>}</span>
      if (n.labhost) { const c = (n.labhost.vms ?? []).length; return <a href={`/labhosts/${n.mac}/vms`} class="text-accent hover:underline">lab host · {c} VM{c === 1 ? '' : 's'}</a> }
      const host = hostOf(n)
      if (host) return <a href={`/labhosts/${host.mac}/vms`} class="text-accent hover:underline">on {hostName(host)}</a>
      return <span class="text-muted">—</span>
    } },
    { id: 'resources', header: 'CPU · RAM · Disk', align: 'right', sort: (n) => n.inventory?.memoryBytes ?? 0, text: (n) => (n.inventory?.disks ?? []).map((d) => d.devPath).join(' '), cell: (n) => { const disks = (n.inventory?.disks ?? []).filter((d) => !d.readonly && !d.cdrom && d.transport !== 'usb'); return n.inventory ? <span class="num whitespace-nowrap" title={disks.map((d) => `${d.devPath} ${fmt.bytes(d.sizeBytes)}`).join(', ')}>{n.inventory.cpus} · {fmt.bytes(n.inventory.memoryBytes)} · {disks.length ? `${fmt.bytes(disks[0].sizeBytes)}${disks.length > 1 ? ` +${disks.length - 1}` : ''}` : '—'}<span class="block text-[10px] text-muted">{n.arch}{n.inventory.kvm ? ' · kvm' : ''}</span></span> : <span class="text-muted">—</span> } },
    { id: 'seen', header: 'Last seen', sort: (n) => n.lastSeen, cell: (n) => <span class="num text-muted">{fmt.when(n.lastSeen)}</span> },
    { id: 'actions', header: '', align: 'right', cell: (n) => (
      <span class="whitespace-nowrap flex gap-1 justify-end">
        {canAdopt(n) && <button class="btn btn-primary !py-1" onClick={() => adopt(n)}>Adopt</button>}
        {canBoot(n) && <button class="btn btn-primary !py-1" disabled={busy[n.mac]} title={n.provision ? 'Armed for a network boot; click to boot again' : 'One network boot via remote management; Talos maintenance mode follows'} onClick={() => boot([n.mac])}>{busy[n.mac] ? 'Starting' : 'Boot into Talos'}</button>}
        {canMakeLabHost(n) && n.kind !== 'maintenance' && <button class="btn !py-1" onClick={() => setLab(n)}>Make lab host</button>}
        {n.wol && !n.host && <button class="btn !py-1" title="Send a Wake-on-LAN magic packet" onClick={() => api.wake(n.mac).then(() => toast('Magic packet sent', 'good')).catch((e) => toast(e.message, 'error'))}>Wake</button>}
        {!n.cluster && canRetire(n) && <button class="btn !py-1" title="Forget this machine" onClick={() => setRetire(n)}>Retire</button>}
        <a href={openHref(n)} class="btn !py-1">Open</a>
      </span>
    ) },
  ]

  return (
    <div class="p-5 flex flex-col gap-4">
      <Section title="Inventory" help="Every physical machine Kubit has seen, by MAC: what it does now and what it can do next."
        actions={<button class="btn btn-primary" onClick={() => setAddAMT(true)} title="Register a machine by its Intel AMT or BMC address">+ Add by remote management</button>}>
        <div class="panel p-3 flex flex-col gap-2">
          <div class="flex gap-2">
            <input class="input mono" value={targets} onInput={(e) => setTargets((e.target as HTMLInputElement).value)} placeholder="192.168.1.0/24, 10.0.0.5" aria-label="Subnets or addresses to scan" />
            <button class="btn btn-primary shrink-0" disabled={scanning || !targets.trim()} onClick={() => api.discover(targets.split(/[,\s]+/).filter(Boolean)).then((r) => watch(r)).catch((e) => toast(e.message, 'error'))}>{scanning ? 'Scanning' : 'Scan'}</button>
          </div>
          <div class="text-[12px] text-muted flex flex-wrap gap-x-3">
            <span>Finds Talos maintenance mode, Intel AMT and Redfish BMCs.</span>
            <span>Boot from USB: <a class="text-accent hover:underline" href={iso('amd64')}>Talos ISO amd64</a> · <a class="text-accent hover:underline" href={iso('arm64')}>arm64</a></span>
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
      {gate && <PxeGate what="Boot into Talos" onClose={() => setGate(null)} onReady={() => { const macs = gate; setGate(null); boot(macs) }} />}
      {retire && <ConfirmDialog title={`Retire ${retire.hostname || retire.mac}`} action="Retire" tone="danger" onClose={() => setRetire(null)}
        onConfirm={() => api.retireMachine(retire.mac).then(() => setRetire(null)).catch((e) => toast(e.message, 'error'))}
        impact={<p>Deletes the inventory row for <span class="mono">{retire.mac}</span> (hardware record, address history). Nothing is sent to the machine; it reappears on the next scan if still online.</p>} />}
    </div>
  )
}
