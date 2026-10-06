import { useMemo } from 'preact/hooks'
import { fmt, type NodeStatus } from '../../api'
import { DataTable, type Column } from '../../components/DataTable'
import { EditAddress } from '../../components/EditAddress'
import { addressOf } from '../../net'
import { useConfigStatus } from '../../configStatus'
import { Pill, Section } from '../../components/ui'
import { machines } from '../../store'
import { modelName, roleLabel, specsOf } from '../../machine'
import { degrees, sensorOf, temperatureTitle, temperatureTone } from '../../temperature'
import { toneText } from '../../tone'
import type { ClusterCtx } from './ClusterPage'

const smallAlloc = 768 * 1048576

export function Nodes({ ctx }: { ctx: ClusterCtx }) {
  const { status, cluster, name } = ctx
  const specs = cluster.spec.spec.nodes
  const apiUp = !!status?.apiReachable
  const behind = useConfigStatus(name)?.behind

  const columns = useMemo<Column<NodeStatus>[]>(() => {
    const specOf = (n: NodeStatus) => specs.find((s) => s.hostname === n.hostname)
    const machineHref = (n: NodeStatus) => { const mac = specOf(n)?.mac; return mac ? `/machines/${mac}` : `/nodes/${n.ip}` }
    const inventoryOf = (n: NodeStatus) => { const mac = specOf(n)?.mac?.toLowerCase(); return mac ? machines.value.get(mac)?.inventory : undefined }
    return [
      { id: 'hostname', header: 'Hostname', sort: (n) => n.hostname, cell: (n) => <a href={machineHref(n)} class="font-medium hover:underline">{n.hostname}</a> },
      { id: 'ip', header: 'Address', sort: (n) => n.ip, mono: true, text: (n) => `${n.ip} ${n.seenAt ?? ''}`, cell: (n) => {
        const sp = specOf(n)
        const moved = n.seenAt && n.seenAt !== n.ip
        const target = addressOf(sp?.network?.addresses[0])
        return (
          <div class="flex flex-col">
            <span>{sp ? <EditAddress cluster={name} hostname={n.hostname}>{n.ip}</EditAddress> : n.ip} <span class="text-[10px] text-muted">{sp?.network ? (sp.network.vlan ? `static · vlan ${sp.network.vlan}` : 'static') : 'dhcp'}</span></span>
            {target && target !== n.ip && <a class="text-[11px] text-info hover:underline" href={`/clusters/${name}/changes`}>→ {target} on apply</a>}
            {moved && <span class="text-[11px] text-warn" title={`Declared ${n.ip}`}>seen at {n.seenAt}</span>}
          </div>
        )
      } },
      { id: 'role', header: 'Role', sort: (n) => `${n.role === 'controlplane' ? 0 : 1} ${n.hostname}`, cell: (n) => roleLabel(n.role) },
      { id: 'hardware', header: 'Hardware', sort: (n) => inventoryOf(n)?.memoryBytes ?? 0, text: (n) => `${modelName(inventoryOf(n))} ${specsOf(inventoryOf(n))}`, cell: (n) => {
        const inv = inventoryOf(n)
        return inv ? (
          <span class="flex flex-col whitespace-nowrap">
            <span>{modelName(inv)}</span>
            <span class="text-[10px] text-muted">{specsOf(inv)}</span>
          </span>
        ) : <span class="text-muted">—</span>
      } },
      { id: 'status', header: 'Status', sort: (n) => (n.talosReachable ? 1 : 0) + (n.ready ? 2 : 0), text: (n) => `${n.talosReachable ? '' : 'unreachable'} ${n.ready ? 'ready' : 'notready'}${behind?.includes(n.hostname) ? ' config behind' : ''}`, cell: (n) => <NodeHealth n={n} apiReachable={apiUp} behind={!!behind?.includes(n.hostname)} changes={`/clusters/${name}/changes`} /> },
      { id: 'talos', header: 'Talos', sort: (n) => n.talosVersion, mono: true, cell: (n) => n.talosVersion || '—' },
      { id: 'kubelet', header: 'Kubelet', sort: (n) => n.kubeletVersion, mono: true, cell: (n) => n.kubeletVersion || '—' },
      { id: 'cpu', header: 'CPU', align: 'right', sort: (n) => n.cpuMilli, cell: (n) => <>{fmt.cores(n.cpuMilli)}<span class="text-muted">/{fmt.cores(n.cpuCapMilli)}</span></> },
      { id: 'ram', header: 'RAM used / total', align: 'right', sort: (n) => n.memBytes, cell: (n) => { const small = !!n.memAllocBytes && n.memAllocBytes < smallAlloc; return <span title={n.memAllocBytes ? `${fmt.bytes(n.memAllocBytes)} allocatable for pods${small ? '; too small for the add-ons' : ''}` : undefined}><span class={n.memCapBytes && n.memBytes >= n.memCapBytes * 0.95 ? 'text-bad' : ''}>{fmt.bytes(n.memBytes)}</span><span class={small ? 'text-warn' : 'text-muted'}>/{fmt.bytes(n.memCapBytes)}</span></span> } },
      { id: 'pods', header: 'Pods', align: 'right', sort: (n) => n.pods, cell: (n) => <a href={`/clusters/${name}/workloads?view=pods&node=${encodeURIComponent(n.hostname)}`} class="hover:underline">{n.pods}</a> },
      { id: 'temp', header: 'Temp', align: 'right', sort: (n) => sensorOf(n.temperatures, 'cpu')?.celsius ?? -1, cell: (n) => <NodeTemperature n={n} /> },
      { id: 'undeclared', header: '', align: 'right', cell: (n) => !specOf(n) && <Pill tone="warn">undeclared</Pill> },
    ]
  }, [specs, apiUp, name, behind, machines.value])

  return (
    <Section title="Nodes" actions={<a class="btn btn-sm" href="/discovery">Add machines</a>}>
      <DataTable id="nodes" columns={columns} rows={status?.nodes ?? []} loading={!status} rowKey={(n) => n.hostname} defaultSort={{ id: 'role', dir: 'asc' }} />
    </Section>
  )
}

function NodeHealth({ n, apiReachable, behind, changes }: { n: NodeStatus; apiReachable: boolean; behind: boolean; changes: string }) {
  const pills = []
  if (!n.talosReachable) pills.push(<Pill key="talos" tone="bad" title={n.talosError}>Talos unreachable</Pill>)
  else if (n.stage && n.stage !== 'running') pills.push(<Pill key="talos" tone="warn">{n.stage}</Pill>)
  if (!apiReachable) pills.push(<Pill key="k8s" tone="muted">k8s unknown</Pill>)
  else if (!n.registered) pills.push(<Pill key="k8s" tone="warn">not registered</Pill>)
  else if (!n.ready) pills.push(<Pill key="k8s" tone="warn">NotReady</Pill>)
  else pills.push(<Pill key="k8s" tone="good">Ready</Pill>)
  if (n.unschedulable) pills.push(<Pill key="cordon" tone="warn">cordoned</Pill>)
  if (behind) pills.push(<a key="config" href={changes}><Pill tone="warn">Config behind</Pill></a>)
  return (
    <div class="flex flex-col gap-0.5">
      <span class="inline-flex flex-wrap gap-1">{pills}</span>
      {!n.talosReachable && n.talosError && <span class="text-[11px] text-muted max-w-[260px] truncate" title={n.talosError}>{n.talosError}</span>}
    </div>
  )
}

function NodeTemperature({ n }: { n: NodeStatus }) {
  const cpu = sensorOf(n.temperatures, 'cpu')
  const disk = sensorOf(n.temperatures, 'disk')
  if (!cpu && !disk) return <span class="text-muted">—</span>
  return (
    <span class="inline-flex flex-col items-end whitespace-nowrap" title={temperatureTitle(n.temperatures!)}>
      {cpu ? <span class={toneText[temperatureTone(cpu)]}>{degrees(cpu.celsius)}</span> : <span class="text-muted">—</span>}
      {disk && <span class={`text-[10px] ${temperatureTone(disk) === 'good' ? 'text-muted' : toneText[temperatureTone(disk)]}`}>disk {degrees(disk.celsius)}</span>}
    </span>
  )
}
