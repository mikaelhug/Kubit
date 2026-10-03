import { useEffect, useState } from 'preact/hooks'
import { useLocation } from 'preact-iso'
import { fmt, type NodeRow } from '../api'
import { AddVMsDialog, HostAlerts, HostMetrics, HostStateNotice, HostSystem, HostVMs, MacSystem, ReleaseHostDialog } from '../components/labhost'
import { RemoteManagement } from '../components/RemoteManagement'
import { Tabs } from '../components/Tabs'
import { identityRows } from '../components/Machine'
import { Action, AlertPill, Breadcrumbs, KeyValue, Pill, Section, SeenAgo } from '../components/ui'
import { hostName, labHostKey, labOffline, labState, modelOf, onMac, vmsOf } from '../machine'
import { subnet24 } from '../net'
import { running } from '../ops'
import { live, machines, openAlerts } from '../store'
import { stateTone } from '../tone'

type TabId = 'overview' | 'vms' | 'actions'
const tabs: { id: TabId; label: string }[] = [{ id: 'overview', label: 'Overview' }, { id: 'vms', label: 'VMs' }, { id: 'actions', label: 'Actions' }]

export function LabHostPage({ mac, tab = 'overview' }: { mac: string; tab?: string }) {
  const { route } = useLocation()
  const host = machines.value.get(mac.toLowerCase()) ?? null
  useEffect(() => { if (host && !host.labhost) route(`/machines/${host.mac}`, true) }, [host?.mac, !!host?.labhost])
  if (!host || !host.labhost) return <div class="p-8 text-muted">{!host && live.value ? `No lab host ${mac} is known.` : 'Loading'}</div>
  const lh = host.labhost
  const shown = (tabs.some((t) => t.id === tab) ? tab : 'overview') as TabId
  const key = labHostKey(host.mac)
  const alert = openAlerts(key)[0]
  const ops = running.value.filter((o) => { const r = o.request as { host?: string; mac?: string } | undefined; return o.cluster === key || r?.host === host.mac || r?.mac === host.mac })
  const busy = ops.length > 0 || lh.state !== 'ready' || labOffline(lh)

  return (
    <div class="flex flex-col">
      <header class="px-6 pt-5 border-b border-border bg-panel">
        <Breadcrumbs items={[{ label: 'Inventory', href: '/fleet/inventory' }, { label: hostName(host) }]} />
        <div class="flex flex-wrap items-center gap-3 mt-2 mb-3">
          <h1 class="text-xl font-semibold">{hostName(host)}</h1>
          <Pill tone={stateTone(labState(lh))} title={lh.error}>{labState(lh)}</Pill>
          {alert?.kind !== 'labhost.unreachable' && <AlertPill e={alert} />}
          {host.oobType && <Pill tone="info">{host.oobType === 'redfish' ? 'BMC' : 'AMT'}</Pill>}
          {ops.map((o) => <Pill key={o.id} tone="warn">{fmt.kind(o.kind)} running</Pill>)}
          {lh.metrics?.at && <SeenAgo contact={lh.metrics.at} blind={lh.failures ? lh.failures >= 3 : false} />}
        </div>
        <Tabs active={shown} tabs={tabs.map((t) => ({ ...t, href: `/labhosts/${host.mac}/${t.id}`, badge: t.id === 'vms' ? vmsOf(lh).length : undefined }))} />
      </header>
      <div class="p-5 flex flex-col gap-4 max-w-[1300px]">
        {shown === 'overview' && <OverviewTab host={host} busy={busy} />}
        {shown === 'vms' && <><HostStateNotice host={host} /><HostVMs host={host} /></>}
        {shown === 'actions' && <ActionsTab host={host} />}
      </div>
    </div>
  )
}

function OverviewTab({ host, busy }: { host: NodeRow; busy: boolean }) {
  const lh = host.labhost!
  const vms = vmsOf(lh)
  return (
    <div class="flex flex-col gap-4">
      <HostStateNotice host={host} />
      <HostAlerts mac={host.mac} />
      {lh.state !== 'installing' && <HostMetrics host={host} lh={lh} />}
      {lh.state !== 'installing' && (onMac(lh) ? <MacSystem host={host} lh={lh} /> : <HostSystem host={host} lh={lh} busy={busy} />)}
      <div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <Section title="Machine">
          <div class="panel p-3">
            <KeyValue rows={[
              ['Model', modelOf(host)],
              ...identityRows(host),
              ['Network', onMac(lh) ? `vmnet ${subnet24(host.ip)}` : lh.network === 'routed' ? 'routed (192.168.123.0/24)' : `bridged on ${lh.capacity.bridge || 'br0'}`],
            ]} />
          </div>
        </Section>
        <Section title="Capacity">
          <div class="panel p-3">
            <KeyValue rows={[
              ['CPUs', String(lh.capacity.cpus || '—')],
              ['Memory', lh.capacity.memMiB ? fmt.bytes(lh.capacity.memMiB * 1048576) : '—'],
              ['VM disk free', `${lh.metrics?.diskTotal ? `${fmt.bytes(lh.metrics.diskTotal - lh.metrics.diskUsed)} of ${fmt.bytes(lh.metrics.diskTotal)}` : lh.capacity.diskGiB ? `${lh.capacity.diskGiB} GiB` : '—'}${lh.disk ? ` on ${lh.disk}` : ''}`],
              onMac(lh) ? ['Hypervisor', lh.capacity.hypervisor || '—'] : ['KVM', lh.capacity.kvm ? 'available' : lh.state === 'ready' ? 'absent' : '—'],
              ['VMs', <a class="text-accent hover:underline" href={`/labhosts/${host.mac}/vms`}>{vms.length} defined{labOffline(lh) ? '' : ` · ${vms.filter((v) => v.state === 'running').length} running`}</a>],
            ]} />
          </div>
        </Section>
      </div>
    </div>
  )
}

function ActionsTab({ host }: { host: NodeRow }) {
  const lh = host.labhost!
  const [addVMs, setAddVMs] = useState(false)
  const [release, setRelease] = useState(false)
  const vms = vmsOf(lh)
  const members = vms.filter((v) => machines.value.get(v.mac)?.cluster).length
  const offline = labOffline(lh)
  return (
    <div class="flex flex-col gap-3 max-w-3xl">
      <Action title="Add VMs" what={`${vms.length} VM${vms.length === 1 ? '' : 's'} defined; new VMs boot Talos maintenance mode.`} button="Add VMs" disabled={lh.state !== 'ready' || offline} onClick={() => setAddVMs(true)} />
      {!onMac(lh) && <Action title="Update or reboot the host" what="Under System on the Overview tab." button="Overview" href={`/labhosts/${host.mac}/overview`} />}
      {!onMac(lh) && <RemoteManagement node={host} />}
      <Action title="Release lab host" what={`Deletes every VM${members ? ` (${members} still in a cluster: remove them first)` : ''}${onMac(lh) ? ' and removes this Mac from Inventory.' : '; Debian stays on the disk.'}`} button="Release" disabled={lh.state === 'installing' || lh.state === 'setup' || lh.state === 'updating' || members > 0} onClick={() => setRelease(true)} />
      {addVMs && <AddVMsDialog host={host} onClose={() => setAddVMs(false)} />}
      {release && <ReleaseHostDialog host={host} onClose={() => setRelease(false)} />}
    </div>
  )
}
