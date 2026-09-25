import { useState } from 'preact/hooks'
import { useLocation } from 'preact-iso'
import { fmt, labHostKey, type NodeRow } from '../api'
import { connected, health, machines, operations, resyncing } from '../store'
import { AddVMsDialog, HostAlerts, HostMetrics, HostStateNotice, HostSystem, HostVMs, MacSystem, ReleaseHostDialog } from '../components/LabHost'
import { RemoteManagement } from '../components/RemoteManagement'
import { Tabs } from '../components/Tabs'
import { AlertPill, Breadcrumbs, KeyValue, Pill, Section, SeenAgo, stateTone } from '../components/ui'
import { hostName, labOffline, labState, modelOf, onMac } from '../machine'

type TabId = 'overview' | 'vms' | 'actions'
const tabs: { id: TabId; label: string }[] = [{ id: 'overview', label: 'Overview' }, { id: 'vms', label: 'VMs' }, { id: 'actions', label: 'Actions' }]

/** One lab host: is it healthy, what VMs does it carry, how is it maintained. */
export function LabHostPage({ mac, tab = 'overview' }: { mac: string; tab?: string }) {
  const { route } = useLocation()
  const host = machines.value.get(mac.toLowerCase()) ?? null
  const loaded = connected.value && !resyncing.value
  if (host && !host.labhost) { route(`/machines/${host.mac}`, true); return null }
  if (!host) return <div class="p-8 text-muted">{loaded ? `No lab host ${mac} is known.` : 'Loading…'}</div>
  const lh = host.labhost!
  const vms = lh.vms ?? []
  const shown = (tabs.some((t) => t.id === tab) ? tab : 'overview') as TabId
  const alert = (health.value.get(labHostKey(host.mac)) ?? []).find((e) => !e.acked && e.severity !== 'info')
  const running = [...operations.value.values()].filter((o) => o.status === 'running' && (o.cluster === labHostKey(host.mac) || (o.request as { host?: string; mac?: string } | undefined)?.host === host.mac || (o.request as { mac?: string } | undefined)?.mac === host.mac))
  const busy = running.length > 0 || lh.state !== 'ready' || labOffline(lh)

  return (
    <div class="flex flex-col">
      <header class="px-6 pt-5 border-b border-border bg-panel">
        <Breadcrumbs items={[{ label: 'Inventory', href: '/fleet/inventory' }, { label: hostName(host) }]} />
        <div class="flex flex-wrap items-center gap-3 mt-2 mb-3">
          <h1 class="text-xl font-semibold">{hostName(host)}</h1>
          <Pill tone={stateTone(labState(lh))} title={lh.error}>{labState(lh)}</Pill>
          {alert?.kind !== 'labhost.unreachable' && <AlertPill e={alert} />}
          {host.oobType && <Pill tone="info">{host.oobType === 'redfish' ? 'BMC' : 'AMT'}</Pill>}
          {running.map((o) => <Pill key={o.id} tone="warn">{fmt.kind(o.kind)} running</Pill>)}
          {lh.metrics?.at && <SeenAgo contact={lh.metrics.at} blind={lh.failures ? lh.failures >= 3 : false} />}
        </div>
        <Tabs active={shown} tabs={tabs.map((t) => ({ ...t, href: `/labhosts/${host.mac}/${t.id}`, badge: t.id === 'vms' ? vms.length : undefined }))} />
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
  const vms = lh.vms ?? []
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
              ['Identity', <span class="mono text-[12px]">{host.mac}{host.uuid ? ` · ${host.uuid}` : ''}{host.serial ? ` · ${host.serial}` : ''}</span>],
              ['Addresses seen', <span class="mono text-[12px]">{[...new Set([...(host.ipsSeen ?? []), host.ip])].filter(Boolean).join(' → ') || '—'}</span>],
              ['Last seen', fmt.datetime(host.lastSeen)],
              ['Network', onMac(lh) ? `vmnet ${host.ip.replace(/\.\d+$/, '.0/24')}` : lh.network === 'routed' ? 'routed (192.168.123.0/24)' : `bridged on ${lh.capacity.bridge || 'br0'}`],
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
  const vms = lh.vms ?? []
  const members = vms.filter((v) => machines.value.get(v.mac)?.cluster).length
  const offline = labOffline(lh)
  return (
    <div class="flex flex-col gap-3 max-w-3xl">
      <Action title="Add VMs" what={`${vms.length} VM${vms.length === 1 ? '' : 's'} defined. New VMs boot Talos in maintenance mode and appear in Inventory.`} button="Add VMs" disabled={lh.state !== 'ready' || offline} onClick={() => setAddVMs(true)} />
      {!onMac(lh) && <Action title="Update or reboot the host" what="Package updates and reboots park the VMs first; both live on the Overview tab under System." button="Overview" href={`/labhosts/${host.mac}/overview`} />}
      {!onMac(lh) && <RemoteManagement node={host} />}
      <Action title="Release lab host" what={`Deletes every VM${members ? ` (${members} still in a cluster: remove them first)` : ''}${onMac(lh) ? ' and its disks, and removes this Mac from Inventory.' : ' and drops the lab-host role. Debian stays on the disk.'}`} button="Release" disabled={lh.state === 'installing' || lh.state === 'setup' || lh.state === 'updating' || members > 0 || offline} onClick={() => setRelease(true)} />
      {addVMs && <AddVMsDialog host={host} onClose={() => setAddVMs(false)} />}
      {release && <ReleaseHostDialog host={host} onClose={() => setRelease(false)} />}
    </div>
  )
}

function Action({ title, what, button, disabled, onClick, href }: { title: string; what: string; button: string; disabled?: boolean; onClick?: () => void; href?: string }) {
  return (
    <div class="panel p-3 flex items-center gap-4">
      <div class="flex-1 min-w-0">
        <div class="font-medium">{title}</div>
        <p class="text-[12.5px] text-muted">{what}</p>
      </div>
      <div class="flex gap-2 shrink-0">
        {href ? <a href={href} class="btn">{button}</a> : <button class="btn btn-primary" disabled={disabled} onClick={onClick}>{button}</button>}
      </div>
    </div>
  )
}
